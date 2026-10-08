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

type Sealer struct {
	aead cipher.AEAD
}

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

func (s *Sealer) Enabled() bool { return s.aead != nil }

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
