// Package ingestauth implements the internal gRPC AuthService that gateway
// collectors call to validate ingest API keys.
package ingestauth

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sag-solutions/otel-fleet/internal/ingestauth/authv1"
	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

// Validation outcomes recorded in otel_fleet_validate_requests_total.
const (
	outcomeOK               = "ok"
	outcomeMalformed        = "malformed"
	outcomeInvalid          = "invalid"
	outcomeExpired          = "expired"
	outcomeCustomerInactive = "customer_inactive"
	outcomeError            = "error"
)

// cacheTTLSeconds is how long the gateway may cache a positive result.
const cacheTTLSeconds = 30

// Store is the persistence subset the auth service needs.
type Store interface {
	ActiveKeysByPrefix(ctx context.Context, prefix string) ([]store.AuthKey, error)
	TouchAPIKeys(ctx context.Context, usages map[uuid.UUID]time.Time) error
}

// Service implements authv1.AuthServiceServer. last_used_at updates are
// batched in memory and flushed periodically (and on shutdown) by Run.
type Service struct {
	authv1.UnimplementedAuthServiceServer

	store      Store
	log        *slog.Logger
	flushEvery time.Duration
	requests   *prometheus.CounterVec

	mu      sync.Mutex
	pending map[uuid.UUID]time.Time
}

// New creates the auth service and registers its metrics with reg.
func New(st Store, log *slog.Logger, reg prometheus.Registerer) *Service {
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "otel_fleet_validate_requests_total",
		Help: "ValidateAPIKey requests by outcome.",
	}, []string{"outcome"})
	reg.MustRegister(requests)
	return &Service{
		store:      st,
		log:        log,
		flushEvery: time.Minute,
		requests:   requests,
		pending:    map[uuid.UUID]time.Time{},
	}
}

// ValidateAPIKey checks a presented ingest API key. Database failures return
// a gRPC error; all verdicts about the key itself return valid=false.
func (s *Service) ValidateAPIKey(ctx context.Context, req *authv1.ValidateAPIKeyRequest) (*authv1.ValidateAPIKeyResponse, error) {
	prefix, ok := tenants.ParseKeyPrefix(req.GetApiKey())
	if !ok {
		s.requests.WithLabelValues(outcomeMalformed).Inc()
		return &authv1.ValidateAPIKeyResponse{Valid: false}, nil
	}

	keys, err := s.store.ActiveKeysByPrefix(ctx, prefix)
	if err != nil {
		s.requests.WithLabelValues(outcomeError).Inc()
		s.log.Error("validate api key: store lookup failed", "err", err)
		return nil, status.Error(codes.Internal, "key lookup failed")
	}

	hash := tenants.HashAPIKey(req.GetApiKey())
	var match *store.AuthKey
	for i := range keys {
		if subtle.ConstantTimeCompare(hash, keys[i].KeyHash) == 1 {
			match = &keys[i]
			break
		}
	}
	switch {
	case match == nil:
		s.requests.WithLabelValues(outcomeInvalid).Inc()
		return &authv1.ValidateAPIKeyResponse{Valid: false}, nil
	case match.ExpiresAt != nil && !match.ExpiresAt.After(time.Now()):
		s.requests.WithLabelValues(outcomeExpired).Inc()
		return &authv1.ValidateAPIKeyResponse{Valid: false}, nil
	case match.CustomerStatus != store.CustomerActive:
		s.requests.WithLabelValues(outcomeCustomerInactive).Inc()
		return &authv1.ValidateAPIKeyResponse{Valid: false}, nil
	}

	s.recordUsage(match.KeyID, time.Now())
	s.requests.WithLabelValues(outcomeOK).Inc()
	return &authv1.ValidateAPIKeyResponse{
		Valid:                true,
		CustomerId:           match.CustomerID.String(),
		ClientId:             match.ClientID,
		CacheTtlSeconds:      cacheTTLSeconds,
		RateLimitItemsPerSec: uint32(match.RateLimitItemsPerSec), //nolint:gosec // DB CHECK keeps it positive and small
	}, nil
}

func (s *Service) recordUsage(keyID uuid.UUID, ts time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.pending[keyID]; !ok || ts.After(prev) {
		s.pending[keyID] = ts
	}
}

// Run flushes batched last_used_at updates every flush interval until ctx is
// cancelled, then performs a final flush.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.Flush(ctx)
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.Flush(flushCtx)
			cancel()
			return
		}
	}
}

// Flush writes all pending last_used_at updates.
func (s *Service) Flush(ctx context.Context) {
	s.mu.Lock()
	batch := s.pending
	s.pending = map[uuid.UUID]time.Time{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := s.store.TouchAPIKeys(ctx, batch); err != nil {
		s.log.Error("flush last_used_at failed", "keys", len(batch), "err", err)
	}
}
