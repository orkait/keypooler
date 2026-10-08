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
// sealedByEarlierCode is "fc-secret" under testKeyHex, written by the per-call
// Encrypt this package used before the Sealer held its own AEAD. Stored keys in
// production were sealed that way and must keep opening.
const sealedByEarlierCode = GCMPrefix + "e114eeeafe68ad08568d25f37a6e35b63d05ad79080690ba4c453542e9a84b7f578400eb50"

func TestAValueSealedByEarlierCodeStillOpens(t *testing.T) {
	s, _ := NewSealer(testKeyHex)
	got, err := s.Open(sealedByEarlierCode)
	if err != nil || got != "fc-secret" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestSealerOpenRefusesWhatItCannotTrust(t *testing.T) {
	keyed, _ := NewSealer(testKeyHex)
	sealed, _ := keyed.Seal("fc-secret")
	plain, _ := NewSealer("")
	otherKey, _ := NewSealer("ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100")
	cases := map[string]struct {
		sealer *Sealer
		stored string
	}{
		"tagged-without-a-key": {plain, sealed},
		"wrong-key":            {otherKey, sealed},
		"shorter-than-a-nonce": {keyed, GCMPrefix + "deadbeef"},
		"not-hex":              {keyed, GCMPrefix + "zz"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.sealer.Open(c.stored); err == nil {
				t.Fatal("must refuse")
			}
		})
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

func BenchmarkSealerOpen(b *testing.B) {
	s, err := NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		b.Fatal(err)
	}
	stored, err := s.Seal("sk-live-0123456789abcdef")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Open(stored); err != nil {
			b.Fatal(err)
		}
	}
}
