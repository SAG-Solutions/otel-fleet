// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantauth // import "github.com/sag-solutions/otel-fleet/collector/extension/tenantauth"

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"
)

// CacheConfig controls the in-memory API-key validation cache.
type CacheConfig struct {
	// TTL is the maximum time a positive validation result is served without
	// re-checking the control plane. The effective TTL per key is
	// min(TTL, ValidateAPIKeyResponse.cache_ttl_seconds) when the server
	// returns a non-zero cache_ttl_seconds.
	TTL time.Duration `mapstructure:"ttl"`
	// NegativeTTL is how long a rejected key stays cached as invalid.
	NegativeTTL time.Duration `mapstructure:"negative_ttl"`
	// MaxEntries bounds the cache size; least-recently-used entries are evicted.
	MaxEntries int `mapstructure:"max_entries"`
	// StaleIfError is how long an expired positive entry may still be served
	// when the control plane is unreachable (fail-open for known keys).
	StaleIfError time.Duration `mapstructure:"stale_if_error"`
}

// Config for the tenantauth server authenticator extension.
type Config struct {
	// Endpoint is the control-plane gRPC address exposing
	// otelfleet.auth.v1.AuthService (e.g. "host.docker.internal:9443").
	Endpoint string `mapstructure:"endpoint"`
	// Insecure disables transport security (dev only; the AuthService
	// listener must never be reachable from outside the cluster).
	Insecure bool `mapstructure:"insecure"`
	// TLS configures transport security when Insecure is false.
	TLS TLSConfig `mapstructure:"tls"`

	Cache CacheConfig `mapstructure:"cache"`
}

// TLSConfig configures the gRPC client transport to the control-plane
// AuthService. When Insecure is false and no CAFile is given, the system trust
// store is used. Setting CertFile+KeyFile enables mutual TLS.
type TLSConfig struct {
	// CAFile is a PEM bundle that must sign the server certificate. Empty =
	// use the system trust store.
	CAFile string `mapstructure:"ca_file"`
	// CertFile / KeyFile present a client certificate (mutual TLS).
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
	// ServerName overrides the SNI / certificate name verified against the
	// server (useful when dialing by IP or a service DNS name).
	ServerName string `mapstructure:"server_name"`
}

// build assembles the client tls.Config: an optional custom CA to verify the
// server, an optional client certificate for mutual TLS, and an optional
// ServerName override.
func (t TLSConfig) build() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName}
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_file %s contains no certificates", t.CAFile)
		}
		cfg.RootCAs = pool
	}
	if t.CertFile != "" || t.KeyFile != "" {
		if t.CertFile == "" || t.KeyFile == "" {
			return nil, errors.New("both cert_file and key_file are required for mutual TLS")
		}
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client key pair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (cfg *Config) Validate() error {
	var errs []error
	if cfg.Endpoint == "" {
		errs = append(errs, errors.New("endpoint must be set to the control-plane AuthService gRPC address"))
	}
	if cfg.Cache.TTL <= 0 {
		errs = append(errs, errors.New("cache.ttl must be positive"))
	}
	if cfg.Cache.NegativeTTL <= 0 {
		errs = append(errs, errors.New("cache.negative_ttl must be positive"))
	}
	if cfg.Cache.MaxEntries <= 0 {
		errs = append(errs, errors.New("cache.max_entries must be positive"))
	}
	if cfg.Cache.StaleIfError < 0 {
		errs = append(errs, errors.New("cache.stale_if_error must not be negative"))
	}
	return errors.Join(errs...)
}
