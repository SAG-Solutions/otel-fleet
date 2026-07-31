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
//
// It supports zero-downtime key rotation: it always Encrypts with the primary
// key but Decrypts by trying the primary and then any secondary (old) keys.
// To rotate, deploy the new key as primary + the old key as a secondary — new
// writes use the new key, old ciphertexts still open — then re-encrypt (see
// ReEncrypt / the rotation guide) and drop the secondary.
type Cipher struct {
	primary    cipher.AEAD
	decrypters []cipher.AEAD // primary first, then secondaries
}

func aeadFromKey(keyBase64 string) (cipher.AEAD, error) {
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
	return aead, nil
}

// New builds a Cipher from the base64-encoded 32-byte master key.
func New(keyBase64 string) (*Cipher, error) {
	return NewWithSecondaries(keyBase64, nil)
}

// NewWithSecondaries builds a Cipher whose primary key encrypts and decrypts,
// plus optional secondary (old) keys used only for decryption during a key
// rotation. Invalid secondary keys are a hard error (fail fast on misconfig).
func NewWithSecondaries(primaryBase64 string, secondariesBase64 []string) (*Cipher, error) {
	primary, err := aeadFromKey(primaryBase64)
	if err != nil {
		return nil, err
	}
	c := &Cipher{primary: primary, decrypters: []cipher.AEAD{primary}}
	for _, s := range secondariesBase64 {
		aead, err := aeadFromKey(s)
		if err != nil {
			return nil, fmt.Errorf("secondary master key: %w", err)
		}
		c.decrypters = append(c.decrypters, aead)
	}
	return c, nil
}

// Configured reports whether a master key is available.
func (c *Cipher) Configured() bool { return c != nil }

// Encrypt seals plaintext with a fresh random nonce.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	nonce := make([]byte, c.primary.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+c.primary.Overhead())
	out = append(out, version1)
	out = append(out, nonce...)
	return c.primary.Seal(out, nonce, plaintext, nil), nil
}

// Decrypt opens a ciphertext produced by Encrypt. Tampered data, a wrong key
// or an unknown version yield ErrDecrypt.
func (c *Cipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	ns := c.primary.NonceSize()
	if len(ciphertext) < 1+ns {
		return nil, ErrDecrypt
	}
	if ciphertext[0] != version1 {
		return nil, ErrDecrypt
	}
	nonce := ciphertext[1 : 1+ns]
	body := ciphertext[1+ns:]
	// Try the primary then any secondary (old) keys — GCM auth fails on the
	// wrong key, so a successful Open identifies the right one.
	for _, aead := range c.decrypters {
		if pt, err := aead.Open(nil, nonce, body, nil); err == nil {
			return pt, nil
		}
	}
	return nil, ErrDecrypt
}

// EncryptedWithPrimary reports whether the ciphertext opens under the PRIMARY
// key (not just a secondary). Used by re-encryption to skip already-migrated
// secrets. A nil cipher or malformed input returns false.
func (c *Cipher) EncryptedWithPrimary(ciphertext []byte) bool {
	if c == nil {
		return false
	}
	ns := c.primary.NonceSize()
	if len(ciphertext) < 1+ns || ciphertext[0] != version1 {
		return false
	}
	_, err := c.primary.Open(nil, ciphertext[1:1+ns], ciphertext[1+ns:], nil)
	return err == nil
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
