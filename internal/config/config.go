// Package config loads the otel-fleet control-plane configuration from
// environment variables. All variables use the OTEL_FLEET_ prefix.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// OIDCProvider describes the env-defined fallback OIDC provider. Additional
// providers are managed in the database (Settings -> SSO) via internal/auth's
// registry; this one keeps single-provider deployments configurable without
// touching the UI.
type OIDCProvider struct {
	// Name is the URL-safe provider identifier (used in /auth/{name}/start).
	Name string
	// DisplayName is shown on the login page.
	DisplayName  string
	Issuer       string
	ClientID     string
	ClientSecret string
}

// Config is the full runtime configuration of the control plane.
type Config struct {
	DatabaseURL string

	ClickHouseAddr     string
	ClickHouseDatabase string
	ClickHouseUser     string
	ClickHousePassword string

	VictoriaMetricsURL string

	// Role selects which listeners/workers this process runs:
	//   all   — everything in one process (default; dev and small deployments)
	//   api   — stateless request tier: HTTP + internal gRPC + ops (scale to N)
	//   opamp — singleton worker tier: OpAMP WebSockets + edge-config listener +
	//           webhook dispatcher + retention sweep + ops
	Role string

	HTTPAddr  string
	GRPCAddr  string
	OpsAddr   string
	OpAMPAddr string
	// OpAMPPublicEndpoint is the externally reachable OpAMP WebSocket URL
	// offered to edge agents in per-agent-token connection settings. Empty =
	// offer only the new auth header (agents keep their current endpoint).
	OpAMPPublicEndpoint string

	// TLS for the public listeners (HTTP :8080 + OpAMP :4320). Empty = plaintext
	// (terminate TLS at an ingress in front, or run dev without it).
	TLSCertFile string
	TLSKeyFile  string
	// TLS for the internal gRPC AuthService (:9443). When GRPCClientCAFile is
	// also set, callers must present a client cert signed by it (mTLS) — this
	// is how gateway collectors authenticate to the API tier.
	GRPCTLSCertFile  string
	GRPCTLSKeyFile   string
	GRPCClientCAFile string
	// OpAMPClientCAFile, when set (with TLS enabled), makes the OpAMP listener
	// (:4320) require and verify a client certificate signed by this CA (mTLS)
	// — a strong control for a public OpAMP endpoint, on top of the enrollment
	// token. Uses the public TLS cert/key for the server side.
	OpAMPClientCAFile string

	BaseURL string
	WebDir  string

	DevLogin      bool
	AdminEmails   []string
	SessionSecure bool

	// SCIMDefaultRole is the role assigned to users provisioned via SCIM
	// (OTEL_FLEET_SCIM_DEFAULT_ROLE); defaults to "viewer" (least privilege).
	// Admins adjust roles/grants afterward in the UI.
	SCIMDefaultRole string

	// MasterKeyBase64 is OTEL_FLEET_MASTER_KEY: the base64-encoded 32-byte key
	// for envelope encryption of secrets at rest (auth-provider client
	// secrets, pipeline exporter credentials). Empty = not configured; the
	// server boots, but features that need it fail with a clear error.
	// Validity (base64, length) is checked by crypto.New at wiring time.
	MasterKeyBase64 string

	// OtelcolBin is the collector distro binary used for `otelcol validate`;
	// when missing, pipeline validation degrades to structural checks.
	OtelcolBin string
	// Distributor selects how rendered forwarding configs are rolled out:
	// "publish" (ops endpoint + collector restart) or "k8s" (patch the
	// OpenTelemetryCollector CR named below).
	Distributor    string
	K8sCRName      string
	K8sCRNamespace string

	// RetentionInterval is how often the per-customer retention sweep runs
	// (OTEL_FLEET_RETENTION_INTERVAL, default 24h).
	RetentionInterval time.Duration

	// Rate limiting for the public HTTP surface (per client IP, token bucket).
	// RateLimit* applies to the whole API/SPA surface; AuthRateLimit* is a
	// stricter bucket layered on the SSO browser endpoints (/auth/*). Disable
	// with OTEL_FLEET_RATE_LIMIT_ENABLED=false when a fronting proxy already
	// rate-limits. MaxRequestBodyBytes caps request bodies (OTLP ingest does
	// not traverse this listener, so a few MiB is ample for the control API).
	RateLimitEnabled    bool
	RateLimitRPS        int
	RateLimitBurst      int
	AuthRateLimitRPS    int
	AuthRateLimitBurst  int
	MaxRequestBodyBytes int64

	// OIDCProviders holds every configured OIDC provider. In Phase 1 at most
	// one (the generic OTEL_FLEET_OIDC_* provider) is present.
	OIDCProviders []OIDCProvider
}

// Load reads the configuration from the process environment.
func Load() (*Config, error) {
	cfg := &Config{
		Role:                env("ROLE", "all"),
		DatabaseURL:         env("DATABASE_URL", "postgres://otelfleet:otelfleet@localhost:5432/otelfleet"),
		ClickHouseAddr:      env("CLICKHOUSE_ADDR", "localhost:9000"),
		ClickHouseDatabase:  env("CLICKHOUSE_DATABASE", "otel"),
		ClickHouseUser:      env("CLICKHOUSE_USER", "otelfleet"),
		ClickHousePassword:  env("CLICKHOUSE_PASSWORD", "otelfleet"),
		VictoriaMetricsURL:  env("VICTORIAMETRICS_URL", "http://localhost:8428"),
		HTTPAddr:            env("HTTP_ADDR", ":8080"),
		GRPCAddr:            env("GRPC_ADDR", ":9443"),
		OpsAddr:             env("OPS_ADDR", ":9090"),
		OpAMPAddr:           env("OPAMP_ADDR", ":4320"),
		OpAMPPublicEndpoint: env("OPAMP_PUBLIC_ENDPOINT", ""),
		TLSCertFile:         env("TLS_CERT_FILE", ""),
		TLSKeyFile:          env("TLS_KEY_FILE", ""),
		GRPCTLSCertFile:     env("GRPC_TLS_CERT_FILE", ""),
		GRPCTLSKeyFile:      env("GRPC_TLS_KEY_FILE", ""),
		GRPCClientCAFile:    env("GRPC_CLIENT_CA_FILE", ""),
		OpAMPClientCAFile:   env("OPAMP_CLIENT_CA_FILE", ""),
		BaseURL:             strings.TrimSuffix(env("BASE_URL", "http://localhost:8080"), "/"),
		WebDir:              env("WEB_DIR", ""),
		OtelcolBin:          env("OTELCOL_BIN", "collector/dist/otel-fleet-collector"),
		Distributor:         env("DISTRIBUTOR", "publish"),
		K8sCRName:           env("K8S_CR_NAME", "otel-fleet-forwarding"),
		K8sCRNamespace:      env("K8S_CR_NAMESPACE", "otelfleet"),
		MasterKeyBase64:     env("MASTER_KEY", ""),
	}
	if cfg.Distributor != "publish" && cfg.Distributor != "k8s" {
		return nil, fmt.Errorf("OTEL_FLEET_DISTRIBUTOR must be 'publish' or 'k8s', got %q", cfg.Distributor)
	}
	if cfg.Role != "all" && cfg.Role != "api" && cfg.Role != "opamp" {
		return nil, fmt.Errorf("OTEL_FLEET_ROLE must be 'all', 'api' or 'opamp', got %q", cfg.Role)
	}

	if raw := env("RETENTION_INTERVAL", "24h"); raw != "" {
		d, perr := time.ParseDuration(raw)
		if perr != nil || d < time.Minute {
			return nil, fmt.Errorf("OTEL_FLEET_RETENTION_INTERVAL: invalid duration %q (min 1m)", raw)
		}
		cfg.RetentionInterval = d
	}

	var err error
	if cfg.RateLimitEnabled, err = envBool("RATE_LIMIT_ENABLED", true); err != nil {
		return nil, err
	}
	if cfg.RateLimitRPS, err = envInt("RATE_LIMIT_RPS", 50); err != nil {
		return nil, err
	}
	if cfg.RateLimitBurst, err = envInt("RATE_LIMIT_BURST", 100); err != nil {
		return nil, err
	}
	if cfg.AuthRateLimitRPS, err = envInt("AUTH_RATE_LIMIT_RPS", 5); err != nil {
		return nil, err
	}
	if cfg.AuthRateLimitBurst, err = envInt("AUTH_RATE_LIMIT_BURST", 10); err != nil {
		return nil, err
	}
	var maxBody int
	if maxBody, err = envInt("MAX_REQUEST_BODY_BYTES", 4<<20); err != nil {
		return nil, err
	}
	cfg.MaxRequestBodyBytes = int64(maxBody)

	if cfg.DevLogin, err = envBool("DEV_LOGIN", false); err != nil {
		return nil, err
	}
	if cfg.SessionSecure, err = envBool("SESSION_SECURE", false); err != nil {
		return nil, err
	}

	for _, e := range strings.Split(env("ADMIN_EMAILS", ""), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			cfg.AdminEmails = append(cfg.AdminEmails, e)
		}
	}

	cfg.SCIMDefaultRole = env("SCIM_DEFAULT_ROLE", "viewer")

	if issuer := env("OIDC_ISSUER", ""); issuer != "" {
		p := OIDCProvider{
			Name:         "oidc",
			DisplayName:  env("OIDC_NAME", "SSO"),
			Issuer:       issuer,
			ClientID:     env("OIDC_CLIENT_ID", ""),
			ClientSecret: env("OIDC_CLIENT_SECRET", ""),
		}
		if p.ClientID == "" {
			return nil, fmt.Errorf("OTEL_FLEET_OIDC_ISSUER is set but OTEL_FLEET_OIDC_CLIENT_ID is empty")
		}
		cfg.OIDCProviders = append(cfg.OIDCProviders, p)
	}

	return cfg, nil
}

// RunsAPI reports whether this process serves the HTTP/gRPC request tier.
func (c *Config) RunsAPI() bool { return c.Role == "all" || c.Role == "api" }

// RunsOpAMP reports whether this process runs the OpAMP server and the
// singleton background workers (edge-config listener, webhooks, retention).
func (c *Config) RunsOpAMP() bool { return c.Role == "all" || c.Role == "opamp" }

// IsAdminEmail reports whether email is listed in OTEL_FLEET_ADMIN_EMAILS.
func (c *Config) IsAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, a := range c.AdminEmails {
		if a == email {
			return true
		}
	}
	return false
}

func env(key, def string) string {
	if v, ok := os.LookupEnv("OTEL_FLEET_" + key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv("OTEL_FLEET_" + key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("OTEL_FLEET_%s: invalid non-negative integer %q", key, v)
	}
	return n, nil
}

func envBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv("OTEL_FLEET_" + key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("OTEL_FLEET_%s: invalid boolean %q", key, v)
	}
	return b, nil
}
