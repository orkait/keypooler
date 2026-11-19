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

const GCMPrefix = "enc:gcm:"

const keySize = 32

// Sealer seals under the current key. During a rotation it also holds the previous key, so values
// sealed under it still open until they are re-sealed (keypool.SealAtRest).
type Sealer struct {
	aead     cipher.AEAD
	previous cipher.AEAD
}

func NewSealer(keyHex, previousHex string) (*Sealer, error) {
	if keyHex == "" {
		if previousHex != "" {
			return nil, errors.New("ENCRYPTION_KEY_PREVIOUS is set without ENCRYPTION_KEY")
		}
		return &Sealer{}, nil
	}
	aead, err := newAEAD("ENCRYPTION_KEY", keyHex)
	if err != nil {
		return nil, err
	}
	s := &Sealer{aead: aead}
	if previousHex != "" {
		if s.previous, err = newAEAD("ENCRYPTION_KEY_PREVIOUS", previousHex); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func newAEAD(name, keyHex string) (cipher.AEAD, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("%s must be valid hex: %w", name, err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("%s must be %d bytes (%d hex chars), got %d", name, keySize, 2*keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	return aead, nil
}

func (s *Sealer) Enabled() bool { return s.aead != nil }

func IsSealed(stored string) bool { return strings.HasPrefix(stored, GCMPrefix) }

func (s *Sealer) Seal(plaintext string) (string, error) {
	if s.aead == nil {
		return plaintext, nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	return GCMPrefix + hex.EncodeToString(s.aead.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

// Current reports whether stored is sealed under the current key, so needs no re-seal.
func (s *Sealer) Current(stored string) bool {
	if s.aead == nil || !IsSealed(stored) {
		return false
	}
	_, err := open(s.aead, stored)
	return err == nil
}

func (s *Sealer) Open(stored string) (string, error) {
	if !IsSealed(stored) {
		return stored, nil
	}
	if s.aead == nil {
		return "", errors.New("value is encrypted but no ENCRYPTION_KEY is configured")
	}
	plaintext, err := open(s.aead, stored)
	if err != nil && s.previous != nil {
		if fromPrevious, previousErr := open(s.previous, stored); previousErr == nil {
			return fromPrevious, nil
		}
	}
	return plaintext, err
}

func open(aead cipher.AEAD, stored string) (string, error) {
	sealed, err := hex.DecodeString(strings.TrimPrefix(stored, GCMPrefix))
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}
	nonceSize := aead.NonceSize()
	if len(sealed) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	plaintext, err := aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}
	return string(plaintext), nil
}
