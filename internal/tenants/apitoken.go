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
	"github.com/sag-solutions/otel-fleet/internal/authz"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Management-API token format: otm_pat_<8 hex>_<43 base64url of 32 bytes>.
// Only the SHA-256 is stored; the prefix is kept in clear for O(1) lookup.

// GenerateAPIToken mints a new management-API token.
func GenerateAPIToken() (GeneratedKey, error) {
	var buf [4 + 32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return GeneratedKey{}, fmt.Errorf("generate api token: %w", err)
	}
	prefix := "otm_pat_" + hex.EncodeToString(buf[:4])
	secret := prefix + "_" + base64.RawURLEncoding.EncodeToString(buf[4:])
	return GeneratedKey{Secret: secret, Prefix: prefix, Hash: HashAPIKey(secret)}, nil
}

// ParseAPITokenPrefix extracts the lookup prefix ("otm_pat_<8 hex>") from a
// presented token. ok is false when the token is not shaped otm_pat_<prefix>_<secret>.
func ParseAPITokenPrefix(token string) (prefix string, ok bool) {
	parts := strings.SplitN(token, "_", 4)
	if len(parts) != 4 || parts[0] != "otm" || parts[1] != "pat" || len(parts[2]) != 8 || parts[3] == "" {
		return "", false
	}
	for _, c := range parts[2] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return "otm_pat_" + parts[2], true
}

// CreatedAPIToken is the result of CreateAPIToken; Secret is returned once.
type CreatedAPIToken struct {
	Token  store.APIToken
	Secret string
}

// CreateAPIToken mints and stores a management-API token with the given role.
func (s *Service) CreateAPIToken(ctx context.Context, actor *uuid.UUID, name, role string, expiresAt *time.Time) (CreatedAPIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return CreatedAPIToken{}, ErrInvalidName
	}
	if !authz.Known(role) {
		return CreatedAPIToken{}, fmt.Errorf("%w: unknown role %q", ErrInvalidName, role)
	}
	tok, err := GenerateAPIToken()
	if err != nil {
		return CreatedAPIToken{}, err
	}
	id := uuid.New()
	stored, err := s.store.CreateAPIToken(ctx, store.NewAPIToken{
		ID:          id,
		Name:        name,
		TokenPrefix: tok.Prefix,
		TokenHash:   tok.Hash,
		Role:        role,
		CreatedBy:   actor,
		ExpiresAt:   expiresAt,
	}, []audit.Entry{{
		ActorUserID: actor,
		Action:      "apitoken.create",
		EntityType:  "api_token",
		EntityID:    id.String(),
		Payload:     map[string]any{"name": name, "role": role, "token_prefix": tok.Prefix},
	}})
	if err != nil {
		return CreatedAPIToken{}, err
	}
	return CreatedAPIToken{Token: stored, Secret: tok.Secret}, nil
}

// RevokeAPIToken revokes a management-API token (idempotent).
func (s *Service) RevokeAPIToken(ctx context.Context, actor *uuid.UUID, tokenID uuid.UUID) error {
	return s.store.RevokeAPIToken(ctx, tokenID, []audit.Entry{{
		ActorUserID: actor,
		Action:      "apitoken.revoke",
		EntityType:  "api_token",
		EntityID:    tokenID.String(),
	}})
}
