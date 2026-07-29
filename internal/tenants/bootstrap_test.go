package tenants

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

func TestGenerateBootstrapTokenRoundtrip(t *testing.T) {
	tok, err := GenerateBootstrapToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.Secret, "otm_bt_") {
		t.Fatalf("secret %q must start with otm_bt_", tok.Secret)
	}
	if !strings.HasPrefix(tok.Secret, tok.Prefix+"_") {
		t.Fatalf("secret %q must start with prefix %q", tok.Secret, tok.Prefix)
	}
	// otm_bt_<8 hex>_<43 base64url>
	parts := strings.SplitN(tok.Secret, "_", 4)
	if len(parts) != 4 || len(parts[2]) != 8 || len(parts[3]) != 43 {
		t.Fatalf("unexpected token shape %q", tok.Secret)
	}

	prefix, ok := ParseBootstrapTokenPrefix(tok.Secret)
	if !ok || prefix != tok.Prefix {
		t.Fatalf("ParseBootstrapTokenPrefix(%q) = %q, %v; want %q, true", tok.Secret, prefix, ok, tok.Prefix)
	}

	sum := sha256.Sum256([]byte(tok.Secret))
	if !bytes.Equal(tok.Hash, sum[:]) {
		t.Error("hash must be SHA-256 of the full secret")
	}
	if !bytes.Equal(HashAPIKey(tok.Secret), tok.Hash) {
		t.Error("HashAPIKey must reproduce the stored hash")
	}
}

func TestGenerateBootstrapTokenUnique(t *testing.T) {
	a, _ := GenerateBootstrapToken()
	b, _ := GenerateBootstrapToken()
	if a.Secret == b.Secret {
		t.Fatal("two generated tokens must differ")
	}
}

func TestParseBootstrapTokenPrefixRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"otm_bt_",
		"otm_bt_abcd1234",       // no secret
		"otm_bt_abcd123_x",      // prefix too short
		"otm_bt_ABCD1234_x",     // uppercase hex
		"otm_bt_zzzz9999_x",     // non-hex
		"otm_abcd1234_secretxx", // API key, not a bootstrap token
		"bearer otm_bt_abcd1234_x",
	} {
		if _, ok := ParseBootstrapTokenPrefix(bad); ok {
			t.Errorf("ParseBootstrapTokenPrefix(%q) must fail", bad)
		}
	}
	if p, ok := ParseBootstrapTokenPrefix("otm_bt_00ff9abc_secret"); !ok || p != "otm_bt_00ff9abc" {
		t.Errorf("valid token rejected: %q %v", p, ok)
	}
}

func TestTokenUsable(t *testing.T) {
	now := time.Now()
	base := store.EnrollToken{
		ExpiresAt:      now.Add(time.Hour),
		MaxUses:        0,
		UsedCount:      100,
		CustomerStatus: store.CustomerActive,
	}
	if err := TokenUsable(base, now); err != nil {
		t.Errorf("unlimited active token must be usable: %v", err)
	}

	expired := base
	expired.ExpiresAt = now.Add(-time.Second)
	if err := TokenUsable(expired, now); err == nil {
		t.Error("expired token must be rejected")
	}

	limited := base
	limited.MaxUses = 3
	limited.UsedCount = 2
	if err := TokenUsable(limited, now); err != nil {
		t.Errorf("token below max_uses must be usable: %v", err)
	}
	limited.UsedCount = 3
	if err := TokenUsable(limited, now); err == nil {
		t.Error("exhausted token must be rejected")
	}

	suspended := base
	suspended.CustomerStatus = store.CustomerSuspended
	if err := TokenUsable(suspended, now); err == nil {
		t.Error("suspended customer's token must be rejected")
	}
}
