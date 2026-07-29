// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSelfSigned writes a throwaway CA/cert PEM pair and returns their paths.
func writeSelfSigned(t *testing.T, dir, name string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, name+"-cert.pem")
	keyPath = filepath.Join(dir, name+"-key.pem")
	certOut, _ := os.Create(certPath)
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	certOut.Close()
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyOut, _ := os.Create(keyPath)
	_ = pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyOut.Close()
	return certPath, keyPath
}

func TestTLSConfigBuild(t *testing.T) {
	dir := t.TempDir()
	caCert, _ := writeSelfSigned(t, dir, "ca")
	clientCert, clientKey := writeSelfSigned(t, dir, "client")

	t.Run("empty uses system roots, TLS 1.2 floor", func(t *testing.T) {
		cfg, err := TLSConfig{}.build()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Errorf("MinVersion = %x, want TLS1.2", cfg.MinVersion)
		}
		if cfg.RootCAs != nil {
			t.Error("expected system roots (nil RootCAs)")
		}
	})

	t.Run("custom CA + server name", func(t *testing.T) {
		cfg, err := TLSConfig{CAFile: caCert, ServerName: "control-plane"}.build()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RootCAs == nil {
			t.Error("custom CA not loaded")
		}
		if cfg.ServerName != "control-plane" {
			t.Errorf("ServerName = %q", cfg.ServerName)
		}
	})

	t.Run("mutual TLS client cert", func(t *testing.T) {
		cfg, err := TLSConfig{CAFile: caCert, CertFile: clientCert, KeyFile: clientKey}.build()
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Certificates) != 1 {
			t.Errorf("client certificate not loaded (%d certs)", len(cfg.Certificates))
		}
	})

	t.Run("cert without key is rejected", func(t *testing.T) {
		if _, err := (TLSConfig{CertFile: clientCert}).build(); err == nil {
			t.Error("expected error for cert_file without key_file")
		}
	})

	t.Run("bad CA file is rejected", func(t *testing.T) {
		if _, err := (TLSConfig{CAFile: clientKey}).build(); err == nil {
			t.Error("expected error for a CA file with no certificates")
		}
	})
}
