// Package tenants implements customer (tenant) and API-key management on top
// of the store. All mutations write audit entries in the same transaction.
package tenants

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Validation errors returned by the service (mapped to 400 by the API layer).
var (
	ErrInvalidName = errors.New("name must be 1-200 characters")
	ErrInvalidSlug = errors.New("slug must match ^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$")
)

// Service implements customer and API-key management.
type Service struct {
	store store.Store
}

// NewService creates a tenants service.
func NewService(st store.Store) *Service { return &Service{store: st} }

// NewClientID mints a tenant client ID ("cust_" + 8 hex chars).
func NewClientID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate client id: %w", err)
	}
	return "cust_" + hex.EncodeToString(b[:]), nil
}

// CreatedCustomer is the result of CreateCustomer. Secret is the full initial
// API key, returned exactly once.
type CreatedCustomer struct {
	Customer store.Customer
	Key      store.APIKey
	Secret   string
}

// CreateCustomer creates a customer plus an initial API key named "default".
// When slug is nil it is derived from the name and deduplicated with a numeric
// suffix; an explicitly provided slug that already exists fails with
// store.ErrSlugExists.
func (s *Service) CreateCustomer(ctx context.Context, actor *uuid.UUID, name string, slug *string) (CreatedCustomer, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return CreatedCustomer{}, ErrInvalidName
	}

	derived := slug == nil
	baseSlug := ""
	if derived {
		baseSlug = DeriveSlug(name)
	} else {
		baseSlug = *slug
		if !ValidSlug(baseSlug) {
			return CreatedCustomer{}, ErrInvalidSlug
		}
	}

	// Retry loop: derived slugs get "-2", "-3", ... on collision; client_id
	// collisions (astronomically rare) simply re-roll.
	const maxAttempts = 10
	trySlug := baseSlug
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		clientID, err := NewClientID()
		if err != nil {
			return CreatedCustomer{}, err
		}
		key, err := GenerateAPIKey()
		if err != nil {
			return CreatedCustomer{}, err
		}

		customerID := uuid.New()
		newCustomer := store.NewCustomer{ID: customerID, Slug: trySlug, Name: name, ClientID: clientID}
		newKey := store.NewAPIKey{
			ID:         uuid.New(),
			CustomerID: customerID,
			Name:       "default",
			KeyPrefix:  key.Prefix,
			KeyHash:    key.Hash,
			CreatedBy:  actor,
		}
		entries := []audit.Entry{
			{
				ActorUserID: actor,
				Action:      "customer.create",
				EntityType:  "customer",
				EntityID:    customerID.String(),
				CustomerID:  &customerID,
				Payload:     map[string]string{"name": name, "slug": trySlug, "client_id": clientID},
			},
			{
				ActorUserID: actor,
				Action:      "apikey.create",
				EntityType:  "api_key",
				EntityID:    newKey.ID.String(),
				CustomerID:  &customerID,
				Payload:     map[string]string{"name": newKey.Name, "key_prefix": key.Prefix},
			},
		}

		cust, apiKey, err := s.store.CreateCustomer(ctx, newCustomer, newKey, entries)
		switch {
		case err == nil:
			return CreatedCustomer{Customer: cust, Key: apiKey, Secret: key.Secret}, nil
		case errors.Is(err, store.ErrSlugExists) && derived:
			trySlug = fmt.Sprintf("%s-%d", baseSlug, attempt+1)
		case errors.Is(err, store.ErrConflict):
			// client_id collision: re-roll and retry with the same slug.
		default:
			return CreatedCustomer{}, err
		}
	}
	return CreatedCustomer{}, fmt.Errorf("could not find a free slug for %q: %w", baseSlug, store.ErrSlugExists)
}

// UpdateCustomer patches name and/or status.
func (s *Service) UpdateCustomer(ctx context.Context, actor *uuid.UUID, id uuid.UUID, upd store.CustomerUpdate) (store.Customer, error) {
	if upd.Name != nil {
		trimmed := strings.TrimSpace(*upd.Name)
		if trimmed == "" || len(trimmed) > 200 {
			return store.Customer{}, ErrInvalidName
		}
		upd.Name = &trimmed
	}
	payload := map[string]any{}
	if upd.Name != nil {
		payload["name"] = *upd.Name
	}
	if upd.Status != nil {
		payload["status"] = *upd.Status
	}
	if upd.RateLimitItemsPerSec.Set {
		payload["rate_limit_items_per_sec"] = upd.RateLimitItemsPerSec.Value // null = cleared
	}
	if upd.RetentionDays.Set {
		payload["retention_days"] = upd.RetentionDays.Value // null = cleared
	}
	return s.store.UpdateCustomer(ctx, id, upd, []audit.Entry{{
		ActorUserID: actor,
		Action:      "customer.update",
		EntityType:  "customer",
		EntityID:    id.String(),
		CustomerID:  &id,
		Payload:     payload,
	}})
}

// DeleteCustomer soft-deletes a customer and revokes all of its API keys.
func (s *Service) DeleteCustomer(ctx context.Context, actor *uuid.UUID, id uuid.UUID) error {
	return s.store.SoftDeleteCustomer(ctx, id, []audit.Entry{{
		ActorUserID: actor,
		Action:      "customer.delete",
		EntityType:  "customer",
		EntityID:    id.String(),
		CustomerID:  &id,
	}})
}

// CreatedKey is the result of CreateAPIKey; Secret is returned exactly once.
type CreatedKey struct {
	Key    store.APIKey
	Secret string
}

// CreateAPIKey mints and stores a new API key for the customer.
func (s *Service) CreateAPIKey(ctx context.Context, actor *uuid.UUID, customerID uuid.UUID, name string, expiresAt *time.Time) (CreatedKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return CreatedKey{}, ErrInvalidName
	}
	key, err := GenerateAPIKey()
	if err != nil {
		return CreatedKey{}, err
	}
	newKey := store.NewAPIKey{
		ID:         uuid.New(),
		CustomerID: customerID,
		Name:       name,
		KeyPrefix:  key.Prefix,
		KeyHash:    key.Hash,
		CreatedBy:  actor,
		ExpiresAt:  expiresAt,
	}
	stored, err := s.store.CreateAPIKey(ctx, newKey, []audit.Entry{{
		ActorUserID: actor,
		Action:      "apikey.create",
		EntityType:  "api_key",
		EntityID:    newKey.ID.String(),
		CustomerID:  &customerID,
		Payload:     map[string]string{"name": name, "key_prefix": key.Prefix},
	}})
	if err != nil {
		return CreatedKey{}, err
	}
	return CreatedKey{Key: stored, Secret: key.Secret}, nil
}

// RevokeAPIKey revokes one key of a customer (idempotent).
func (s *Service) RevokeAPIKey(ctx context.Context, actor *uuid.UUID, customerID, keyID uuid.UUID) error {
	return s.store.RevokeAPIKey(ctx, customerID, keyID, []audit.Entry{{
		ActorUserID: actor,
		Action:      "apikey.revoke",
		EntityType:  "api_key",
		EntityID:    keyID.String(),
		CustomerID:  &customerID,
	}})
}
