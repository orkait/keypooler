package crypto

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// GCMPrefix tags a stored value as AES-256-GCM ciphertext (hex-encoded after the
// prefix). Plaintext values are stored with no prefix. Self-tagging lets encrypted
// and plaintext values coexist in one database, and lets the encryption mode be
// turned on later without migrating existing plaintext rows.
const GCMPrefix = "enc:gcm:"

// Sealer is the single place that decides plaintext-vs-encrypted for values at
// rest. Encryption is opt-in: an empty key selects plaintext mode (the default),
// a configured key encrypts new writes. Reads are mode-independent - a value is
// decrypted iff it carries the GCMPrefix tag.
type Sealer struct {
	keyHex string
}

// NewSealer builds a Sealer. An empty keyHex selects plaintext mode. A non-empty
// keyHex must be 32 bytes of hex (64 chars, AES-256); anything else is a config
// error surfaced at startup.
func NewSealer(keyHex string) (*Sealer, error) {
	if keyHex != "" {
		raw, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, fmt.Errorf("ENCRYPTION_KEY must be valid hex: %w", err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("ENCRYPTION_KEY must be 32 bytes (64 hex chars), got %d", len(raw))
		}
	}
	return &Sealer{keyHex: keyHex}, nil
}

// Enabled reports whether encryption is on (a key is configured).
func (s *Sealer) Enabled() bool { return s.keyHex != "" }

// Seal encodes a plaintext value for storage. Encryption on -> GCMPrefix +
// hex(ciphertext). Encryption off -> the plaintext unchanged (no tag).
func (s *Sealer) Seal(plaintext string) (string, error) {
	if s.keyHex == "" {
		return plaintext, nil
	}
	ct, err := Encrypt(plaintext, s.keyHex)
	if err != nil {
		return "", err
	}
	return GCMPrefix + ct, nil
}

// Open decodes a stored value. A GCMPrefix-tagged value is decrypted (which
// requires a configured key; a tagged value with no key is a hard error so a
// misconfiguration can never silently serve ciphertext). An untagged value is
// plaintext and returned as-is.
func (s *Sealer) Open(stored string) (string, error) {
	if !strings.HasPrefix(stored, GCMPrefix) {
		return stored, nil
	}
	if s.keyHex == "" {
		return "", fmt.Errorf("value is encrypted but no ENCRYPTION_KEY is configured")
	}
	return Decrypt(strings.TrimPrefix(stored, GCMPrefix), s.keyHex)
}
