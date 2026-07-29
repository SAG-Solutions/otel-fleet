// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

// Package tenantauth implements a collector server authenticator that
// validates ingest API keys against the otel-fleet control plane
// (otelfleet.auth.v1.AuthService) and attaches the resolved tenant identity
// to client.Info so downstream processors (tenantstamp) can stamp it onto
// telemetry resources.
package tenantauth // import "github.com/sag-solutions/otel-fleet/collector/extension/tenantauth"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensionauth"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sag-solutions/otel-fleet/collector/extension/tenantauth/internal/authv1"
)

// validateTimeout bounds a single control-plane ValidateAPIKey call so a slow
// control plane cannot stall ingest indefinitely.
const validateTimeout = 5 * time.Second

// Outcome attribute values for otel_fleet_auth_requests_total.
const (
	outcomeOK                  = "ok"
	outcomeInvalidKey          = "invalid_key"
	outcomeNoKey               = "no_key"
	outcomeUpstreamErrorStale  = "upstream_error_stale"
	outcomeUpstreamErrorReject = "upstream_error_reject"
)

var (
	errNoKey      = errors.New("no API key provided: expected 'Authorization: Bearer <key>' or 'X-Api-Key' header")
	errInvalidKey = errors.New("invalid API key")
)

var (
	_ extension.Extension  = (*authenticator)(nil)
	_ extensionauth.Server = (*authenticator)(nil)
)

type authenticator struct {
	cfg       *Config
	telemetry component.TelemetrySettings
	cache     *keyCache

	conn   *grpc.ClientConn
	authSvc authv1.AuthServiceClient

	requestsTotal metric.Int64Counter

	// now is swappable in tests.
	now func() time.Time
}

func newAuthenticator(cfg *Config, telemetry component.TelemetrySettings) (*authenticator, error) {
	a := &authenticator{
		cfg:       cfg,
		telemetry: telemetry,
		cache:     newKeyCache(cfg.Cache.MaxEntries),
		now:       time.Now,
	}
	meter := telemetry.MeterProvider.Meter("github.com/sag-solutions/otel-fleet/collector/extension/tenantauth")
	var err error
	a.requestsTotal, err = meter.Int64Counter(
		"otel_fleet_auth_requests_total",
		metric.WithDescription("API key authentication attempts, by outcome and tenant (when known)."),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating otel_fleet_auth_requests_total counter: %w", err)
	}
	return a, nil
}

func (a *authenticator) Start(_ context.Context, _ component.Host) error {
	var creds credentials.TransportCredentials
	if a.cfg.Insecure {
		creds = insecure.NewCredentials()
	} else {
		tlsCfg, err := a.cfg.TLS.build()
		if err != nil {
			return fmt.Errorf("tenantauth TLS: %w", err)
		}
		creds = credentials.NewTLS(tlsCfg)
	}
	conn, err := grpc.NewClient(a.cfg.Endpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		return fmt.Errorf("creating AuthService client for %q: %w", a.cfg.Endpoint, err)
	}
	a.conn = conn
	a.authSvc = authv1.NewAuthServiceClient(conn)
	return nil
}

func (a *authenticator) Shutdown(_ context.Context) error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

// Authenticate implements extensionauth.Server. It extracts the API key from
// the request headers, validates it (cache first, then the control plane) and,
// on success, returns a context whose client.Info carries the tenant identity.
func (a *authenticator) Authenticate(ctx context.Context, sources map[string][]string) (context.Context, error) {
	key := extractKey(sources)
	if key == "" {
		a.count(ctx, outcomeNoKey, "")
		return ctx, errNoKey
	}

	sum := sha256.Sum256([]byte(key))
	hash := hex.EncodeToString(sum[:])
	now := a.now()

	if e, ok := a.cache.get(hash); ok && now.Before(e.freshUntil) {
		if !e.valid {
			a.count(ctx, outcomeInvalidKey, "")
			return ctx, errInvalidKey
		}
		a.count(ctx, outcomeOK, e.data.tenantID)
		return contextWithAuth(ctx, e.data), nil
	}

	rpcCtx, cancel := context.WithTimeout(ctx, validateTimeout)
	resp, err := a.authSvc.ValidateAPIKey(rpcCtx, &authv1.ValidateAPIKeyRequest{ApiKey: key})
	cancel()
	if err != nil {
		// Control plane unreachable or erroring: fail open for keys we have
		// recently seen as valid (up to stale_if_error), fail closed otherwise.
		if e, ok := a.cache.get(hash); ok && e.valid && now.Before(e.staleUntil) {
			a.count(ctx, outcomeUpstreamErrorStale, e.data.tenantID)
			return contextWithAuth(ctx, e.data), nil
		}
		a.count(ctx, outcomeUpstreamErrorReject, "")
		return ctx, fmt.Errorf("API key validation unavailable: %w", err)
	}

	if !resp.GetValid() {
		a.cache.put(hash, cacheEntry{
			valid:      false,
			freshUntil: now.Add(a.cfg.Cache.NegativeTTL),
		})
		a.count(ctx, outcomeInvalidKey, "")
		return ctx, errInvalidKey
	}

	ttl := a.cfg.Cache.TTL
	if s := resp.GetCacheTtlSeconds(); s > 0 {
		if serverTTL := time.Duration(s) * time.Second; serverTTL < ttl {
			ttl = serverTTL
		}
	}
	data := &authData{
		// The control plane's client_id is the tenant identity stamped as
		// resource attribute tenant.id (see proto/authservice.proto).
		tenantID:   resp.GetClientId(),
		clientID:   resp.GetClientId(),
		customerID: resp.GetCustomerId(),
		// Per-tenant ingest quota enforced by the tenantquota processor;
		// 0 = unlimited. Rides the cache entry, so changes propagate within
		// the (fresh) cache TTL.
		rateLimitItemsPerSec: int64(resp.GetRateLimitItemsPerSec()),
	}
	a.cache.put(hash, cacheEntry{
		valid:      true,
		data:       data,
		freshUntil: now.Add(ttl),
		staleUntil: now.Add(a.cfg.Cache.StaleIfError),
	})
	a.count(ctx, outcomeOK, data.tenantID)
	return contextWithAuth(ctx, data), nil
}

func contextWithAuth(ctx context.Context, data *authData) context.Context {
	info := client.FromContext(ctx)
	info.Auth = data
	return client.NewContext(ctx, info)
}

// extractKey pulls the API key from `authorization: Bearer <key>` or
// `x-api-key` headers (names case-insensitive; bearer scheme case-insensitive).
func extractKey(sources map[string][]string) string {
	var bearer, apiKey string
	for name, values := range sources {
		switch strings.ToLower(name) {
		case "authorization":
			for _, v := range values {
				scheme, rest, found := strings.Cut(strings.TrimSpace(v), " ")
				if found && strings.EqualFold(scheme, "Bearer") {
					if k := strings.TrimSpace(rest); k != "" && bearer == "" {
						bearer = k
					}
				}
			}
		case "x-api-key":
			for _, v := range values {
				if k := strings.TrimSpace(v); k != "" && apiKey == "" {
					apiKey = k
				}
			}
		}
	}
	if bearer != "" {
		return bearer
	}
	return apiKey
}

func (a *authenticator) count(ctx context.Context, outcome, tenantID string) {
	attrs := []attribute.KeyValue{attribute.String("outcome", outcome)}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	a.requestsTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
}
