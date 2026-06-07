package crypto

import (
	"strings"
	"testing"
)

// a valid 32-byte AES-256 key as 64 hex chars
const testKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestSealerPlaintextMode(t *testing.T) {
	s, err := NewSealer("")
	if err != nil {
		t.Fatalf("NewSealer(\"\"): %v", err)
	}
	if s.Enabled() {
		t.Fatal("empty key must select plaintext mode")
	}
	sealed, err := s.Seal("fc-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed != "fc-secret" {
		t.Fatalf("plaintext mode must store value unchanged, got %q", sealed)
	}
	if strings.HasPrefix(sealed, GCMPrefix) {
		t.Fatal("plaintext value must not carry the enc tag")
	}
	out, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if out != "fc-secret" {
		t.Fatalf("round-trip mismatch: %q", out)
	}
}

func TestSealerEncryptedRoundTrip(t *testing.T) {
	s, err := NewSealer(testKeyHex)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	if !s.Enabled() {
		t.Fatal("configured key must enable encryption")
	}
	sealed, err := s.Seal("fc-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(sealed, GCMPrefix) {
		t.Fatalf("encrypted value must carry the enc tag, got %q", sealed)
	}
	if strings.Contains(sealed, "fc-secret") {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	out, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if out != "fc-secret" {
		t.Fatalf("round-trip mismatch: %q", out)
	}
}

// An encrypted sealer must still pass through an untagged (plaintext) value, so a
// pool with mixed plaintext + encrypted rows works after encryption is turned on.
func TestSealerEncryptedOpensPlaintext(t *testing.T) {
	s, _ := NewSealer(testKeyHex)
	out, err := s.Open("fc-plain")
	if err != nil {
		t.Fatalf("Open untagged: %v", err)
	}
	if out != "fc-plain" {
		t.Fatalf("untagged value must pass through, got %q", out)
	}
}

// A tagged value with no key configured must be a hard error, never silently
// served as ciphertext.
func TestSealerTaggedWithoutKeyErrors(t *testing.T) {
	plain, _ := NewSealer("")
	if _, err := plain.Open(GCMPrefix + "deadbeef"); err == nil {
		t.Fatal("opening an encrypted value with no key must error")
	}
}

func TestSealerWrongKeyFails(t *testing.T) {
	s, _ := NewSealer(testKeyHex)
	sealed, _ := s.Seal("fc-secret")
	other, _ := NewSealer("ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100")
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("decrypting with the wrong key must fail the GCM auth tag")
	}
}

func TestNewSealerRejectsBadKey(t *testing.T) {
	if _, err := NewSealer("xyz"); err == nil {
		t.Fatal("non-hex key must error")
	}
	if _, err := NewSealer("00112233"); err == nil {
		t.Fatal("short key must error")
	}
}
