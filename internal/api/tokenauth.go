package api

import (
	"context"
	"crypto/subtle"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

// tokenStore is the store subset needed to validate management-API tokens.
type tokenStore interface {
	ActiveAPITokensByPrefix(ctx context.Context, prefix string) ([]store.APITokenAuth, error)
}

// authenticateAPIToken validates a presented management-API token. It returns
// the token's role and its creator (for audit attribution) on success. Only
// otm_pat_ tokens are considered; anything else returns ok=false so the caller
// falls back to session auth.
func authenticateAPIToken(ctx context.Context, ts tokenStore, authorization string) (role string, createdBy *uuid.UUID, ok bool) {
	raw, found := strings.CutPrefix(authorization, "Bearer ")
	if !found || raw == "" {
		return "", nil, false
	}
	prefix, isPAT := tenants.ParseAPITokenPrefix(raw)
	if !isPAT {
		return "", nil, false
	}
	toks, err := ts.ActiveAPITokensByPrefix(ctx, prefix)
	if err != nil {
		return "", nil, false
	}
	hash := tenants.HashAPIKey(raw)
	now := time.Now()
	for i := range toks {
		if subtle.ConstantTimeCompare(hash, toks[i].TokenHash) == 1 {
			if toks[i].ExpiresAt != nil && !toks[i].ExpiresAt.After(now) {
				return "", nil, false
			}
			return toks[i].Role, toks[i].CreatedBy, true
		}
	}
	return "", nil, false
}

// looksLikeAPIToken reports whether the Authorization header carries an
// otm_pat_ token (so the Guard routes it to token auth, not session auth).
func looksLikeAPIToken(authorization string) bool {
	raw, found := strings.CutPrefix(authorization, "Bearer ")
	if !found {
		return false
	}
	_, ok := tenants.ParseAPITokenPrefix(raw)
	return ok
}
