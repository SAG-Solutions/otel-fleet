// Package crypto implements envelope encryption for secrets at rest
// (auth-provider client secrets, pipeline exporter credentials) with
// AES-256-GCM keyed by OTEL_FLEET_MASTER_KEY.
//
// Ciphertext layout: [1 version byte][12-byte random nonce][GCM ciphertext].
// The version byte allows future key rotation / algorithm changes; the only
// version today is 0x01.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// version1 is the only ciphertext format so far: AES-256-GCM, nonce prepended.
const version1 = 0x01

// KeySize is the required master key length after base64 decoding.
const KeySize = 32

// ErrNotConfigured is returned by a nil *Cipher: the feature needs the master
// key but OTEL_FLEET_MASTER_KEY is not set.
var ErrNotConfigured = errors.New("master key not configured (set OTEL_FLEET_MASTER_KEY)")

// ErrDecrypt is returned when a ciphertext cannot be decrypted (tampered
// data, wrong key or unknown format version).
var ErrDecrypt = errors.New("cannot decrypt: data corrupted or wrong master key")

// Cipher encrypts and decrypts secrets with the master key. A nil *Cipher is
// valid and returns ErrNotConfigured from both operations, so callers can
// thread it through unconditionally.
type Cipher struct {
	aead cipher.AEAD
}

// New builds a Cipher from the base64-encoded 32-byte master key.
func New(keyBase64 string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("master key is not valid base64: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("master key must be %d bytes after base64 decoding, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Configured reports whether a master key is available.
func (c *Cipher) Configured() bool { return c != nil }

// Encrypt seals plaintext with a fresh random nonce.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, version1)
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, plaintext, nil), nil
}

// Decrypt opens a ciphertext produced by Encrypt. Tampered data, a wrong key
// or an unknown version yield ErrDecrypt.
func (c *Cipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	if len(ciphertext) < 1+c.aead.NonceSize() {
		return nil, ErrDecrypt
	}
	if ciphertext[0] != version1 {
		return nil, ErrDecrypt
	}
	nonce := ciphertext[1 : 1+c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext[1+c.aead.NonceSize():], nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// NewRandomKeyBase64 generates a fresh master key, base64-encoded — used in
// error hints and setup docs (`OTEL_FLEET_MASTER_KEY=<value>`).
func NewRandomKeyBase64() string {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return base64.StdEncoding.EncodeToString(key)
}
