package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	saml2 "github.com/russellhaering/gosaml2"
)

// testCertPEM generates a throwaway self-signed certificate for parsing tests.
func testCertPEM(t *testing.T) (pemStr, b64DER string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-idp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemStr = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	b64DER = base64.StdEncoding.EncodeToString(der)
	return pemStr, b64DER
}

func TestParseCertificate(t *testing.T) {
	pemStr, b64 := testCertPEM(t)

	if _, err := parseCertificate(pemStr); err != nil {
		t.Errorf("PEM: %v", err)
	}
	if _, err := parseCertificate(b64); err != nil {
		t.Errorf("bare base64 DER: %v", err)
	}
	// base64 with whitespace/newlines, as IdP metadata often wraps it.
	wrapped := b64[:20] + "\n" + b64[20:40] + " " + b64[40:]
	if _, err := parseCertificate(wrapped); err != nil {
		t.Errorf("wrapped base64 DER: %v", err)
	}
	if _, err := parseCertificate(""); err == nil {
		t.Error("empty cert should error")
	}
	if _, err := parseCertificate("not-a-cert"); err == nil {
		t.Error("garbage should error")
	}
}

func TestValidateSAMLConfig(t *testing.T) {
	pemStr, _ := testCertPEM(t)
	good := SAMLConfig{IDPEntityID: "https://idp.example.com", IDPSSOURL: "https://idp.example.com/sso", IDPCertificate: pemStr}
	if err := ValidateSAMLConfig(good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []SAMLConfig{
		{IDPSSOURL: "https://idp/sso", IDPCertificate: pemStr},                  // no entity id
		{IDPEntityID: "e", IDPSSOURL: "http://idp/sso", IDPCertificate: pemStr}, // http not https
		{IDPEntityID: "e", IDPSSOURL: "https://idp/sso"},                        // no cert
	}
	for i, c := range cases {
		if err := ValidateSAMLConfig(c); err == nil {
			t.Errorf("case %d should be invalid", i)
		}
	}
}

func newSAMLHandler(t *testing.T) *SAMLHandler {
	t.Helper()
	pemStr, _ := testCertPEM(t)
	info := ProviderInfo{
		Type: TypeSAML, Name: "okta",
		SAML: &SAMLConfig{
			IDPEntityID:    "https://idp.example.com/entity",
			IDPSSOURL:      "https://idp.example.com/sso",
			IDPCertificate: pemStr,
		},
	}
	f := loginFinisher{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := NewSAMLHandler(info, "https://otel-fleet.example.com", f)
	if h.buildErr != nil {
		t.Fatalf("build sp: %v", h.buildErr)
	}
	return h
}

func TestSAMLStartRedirectsToIdP(t *testing.T) {
	h := newSAMLHandler(t)
	rec := httptest.NewRecorder()
	h.Start(rec, httptest.NewRequest(http.MethodGet, "/auth/okta/start", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme+"://"+u.Host+u.Path != "https://idp.example.com/sso" {
		t.Errorf("redirect base = %q, want the IdP SSO URL", u.Scheme+"://"+u.Host+u.Path)
	}
	if u.Query().Get("SAMLRequest") == "" {
		t.Error("redirect is missing the SAMLRequest parameter")
	}
}

func TestSAMLMetadataServesXML(t *testing.T) {
	h := newSAMLHandler(t)
	rec := httptest.NewRecorder()
	h.Metadata(rec, httptest.NewRequest(http.MethodGet, "/auth/okta/metadata", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "EntityDescriptor") {
		t.Error("metadata is not an EntityDescriptor")
	}
	// The SP entity id and ACS URL must be present for the IdP to consume.
	if !strings.Contains(body, "https://otel-fleet.example.com/auth/okta/metadata") {
		t.Error("metadata missing SP entity id")
	}
	if !strings.Contains(body, "https://otel-fleet.example.com/auth/okta/acs") {
		t.Error("metadata missing ACS URL")
	}
}

func TestSAMLACSRejectsMissingResponse(t *testing.T) {
	h := newSAMLHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/okta/acs", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ACS(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty ACS POST status = %d, want 400", rec.Code)
	}
}

func TestSAMLEmailAndDisplayNameExtraction(t *testing.T) {
	// NameID that is an email is used directly.
	a := &saml2.AssertionInfo{NameID: "alice@example.com"}
	if got := samlEmail(a); got != "alice@example.com" {
		t.Errorf("email from NameID = %q", got)
	}
	// Otherwise fall back to an email attribute; displayName from attributes.
	a = &saml2.AssertionInfo{NameID: "opaque-name-id", Values: saml2.Values{}}
	if got := samlEmail(a); got != "" {
		t.Errorf("no email attribute should yield empty, got %q", got)
	}
}
