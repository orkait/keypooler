package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// GCMPrefix tags a stored value as AES-256-GCM ciphertext (hex-encoded after the
// prefix). Plaintext values are stored with no prefix. Self-tagging lets encrypted
// and plaintext values coexist in one database, and lets the encryption mode be
// turned on later without migrating existing plaintext rows.
const GCMPrefix = "enc:gcm:"

// keySize is AES-256's key length in bytes.
const keySize = 32

// Sealer is the single place that decides plaintext-vs-encrypted for values at
// rest. Encryption is opt-in: an empty key selects plaintext mode (the default),
// a configured key encrypts new writes. Reads are mode-independent - a value is
// decrypted iff it carries the GCMPrefix tag.
type Sealer struct {
	// aead is built once from the key and is nil in plaintext mode. GCM keeps no
	// per-call state, so one instance serves every request.
	aead cipher.AEAD
}

// NewSealer builds a Sealer. An empty keyHex selects plaintext mode. A non-empty
// keyHex must be 32 bytes of hex (64 chars, AES-256); anything else is a config
// error surfaced at startup.
func NewSealer(keyHex string) (*Sealer, error) {
	if keyHex == "" {
		return &Sealer{}, nil
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be valid hex: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be %d bytes (%d hex chars), got %d", keySize, 2*keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Enabled reports whether encryption is on (a key is configured).
func (s *Sealer) Enabled() bool { return s.aead != nil }

// Seal encodes a plaintext value for storage. Encryption on -> GCMPrefix +
// hex(nonce || ciphertext). Encryption off -> the plaintext unchanged (no tag).
func (s *Sealer) Seal(plaintext string) (string, error) {
	if s.aead == nil {
		return plaintext, nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return GCMPrefix + hex.EncodeToString(sealed), nil
}

// Open decodes a stored value. A GCMPrefix-tagged value is decrypted (which
// requires a configured key; a tagged value with no key is a hard error so a
// misconfiguration can never silently serve ciphertext). An untagged value is
// plaintext and returned as-is.
func (s *Sealer) Open(stored string) (string, error) {
	hexed, tagged := strings.CutPrefix(stored, GCMPrefix)
	if !tagged {
		return stored, nil
	}
	if s.aead == nil {
		return "", errors.New("value is encrypted but no ENCRYPTION_KEY is configured")
	}
	sealed, err := hex.DecodeString(hexed)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}
	nonceSize := s.aead.NonceSize()
	if len(sealed) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	plaintext, err := s.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}
	return string(plaintext), nil
}
