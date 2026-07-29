package tenants

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Bootstrap token format: otm_bt_<8 lowercase hex>_<43 chars base64url of 32
// random bytes>. Only the SHA-256 of the full token is stored; the prefix
// ("otm_bt_<hex>") is kept in clear for display and lookup. Mirrors the
// ingest API key format (otm_<hex>_<secret>).

// defaultBootstrapTokenTTL applies when no explicit expiry is given.
const defaultBootstrapTokenTTL = 30 * 24 * time.Hour

// GenerateBootstrapToken mints a new edge-agent enrollment token.
func GenerateBootstrapToken() (GeneratedKey, error) {
	var buf [4 + 32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return GeneratedKey{}, fmt.Errorf("generate bootstrap token: %w", err)
	}
	prefix := "otm_bt_" + hex.EncodeToString(buf[:4])
	secret := prefix + "_" + base64.RawURLEncoding.EncodeToString(buf[4:])
	return GeneratedKey{Secret: secret, Prefix: prefix, Hash: HashAPIKey(secret)}, nil
}

// ParseBootstrapTokenPrefix extracts the lookup prefix ("otm_bt_<8 hex>") from
// a presented token. ok is false when the token does not have the
// otm_bt_<prefix>_<secret> shape.
func ParseBootstrapTokenPrefix(token string) (prefix string, ok bool) {
	parts := strings.SplitN(token, "_", 4)
	if len(parts) != 4 || parts[0] != "otm" || parts[1] != "bt" || len(parts[2]) != 8 || parts[3] == "" {
		return "", false
	}
	for _, c := range parts[2] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return "otm_bt_" + parts[2], true
}

// TokenUsable reports whether an enrollment token may authenticate a new
// OpAMP connection at the given time (the constant-time hash comparison has
// already succeeded).
func TokenUsable(t store.EnrollToken, now time.Time) error {
	switch {
	case !t.ExpiresAt.After(now):
		return fmt.Errorf("token expired at %s", t.ExpiresAt.Format(time.RFC3339))
	case t.MaxUses > 0 && t.UsedCount >= t.MaxUses:
		return fmt.Errorf("token exhausted (%d/%d uses)", t.UsedCount, t.MaxUses)
	case t.CustomerStatus != store.CustomerActive:
		return fmt.Errorf("customer is %s", t.CustomerStatus)
	}
	return nil
}

// CreatedBootstrapToken is the result of CreateBootstrapToken; Secret is
// returned exactly once.
type CreatedBootstrapToken struct {
	Token  store.BootstrapToken
	Secret string
}

// CreateBootstrapToken mints and stores a new enrollment token for the
// customer. A nil expiresAt defaults to 30 days.
func (s *Service) CreateBootstrapToken(ctx context.Context, actor *uuid.UUID, customerID uuid.UUID, name string, expiresAt *time.Time, maxUses int) (CreatedBootstrapToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return CreatedBootstrapToken{}, ErrInvalidName
	}
	if maxUses < 0 {
		maxUses = 0
	}
	tok, err := GenerateBootstrapToken()
	if err != nil {
		return CreatedBootstrapToken{}, err
	}
	expiry := time.Now().Add(defaultBootstrapTokenTTL)
	if expiresAt != nil {
		expiry = *expiresAt
	}
	newToken := store.NewBootstrapToken{
		ID:          uuid.New(),
		CustomerID:  customerID,
		Name:        name,
		TokenPrefix: tok.Prefix,
		TokenHash:   tok.Hash,
		MaxUses:     maxUses,
		CreatedBy:   actor,
		ExpiresAt:   expiry,
	}
	stored, err := s.store.CreateBootstrapToken(ctx, newToken, []audit.Entry{{
		ActorUserID: actor,
		Action:      "bootstraptoken.create",
		EntityType:  "bootstrap_token",
		EntityID:    newToken.ID.String(),
		CustomerID:  &customerID,
		Payload:     map[string]any{"name": name, "token_prefix": tok.Prefix, "max_uses": maxUses},
	}})
	if err != nil {
		return CreatedBootstrapToken{}, err
	}
	return CreatedBootstrapToken{Token: stored, Secret: tok.Secret}, nil
}

// RevokeBootstrapToken revokes one enrollment token of a customer
// (idempotent). Already-enrolled agents stay connected.
func (s *Service) RevokeBootstrapToken(ctx context.Context, actor *uuid.UUID, customerID, tokenID uuid.UUID) error {
	return s.store.RevokeBootstrapToken(ctx, customerID, tokenID, []audit.Entry{{
		ActorUserID: actor,
		Action:      "bootstraptoken.revoke",
		EntityType:  "bootstrap_token",
		EntityID:    tokenID.String(),
		CustomerID:  &customerID,
	}})
}
