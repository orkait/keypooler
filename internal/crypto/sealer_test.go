package crypto

import (
	"strings"
	"testing"
)

const testKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestAValueRoundTripsTaggedOnlyWhenEncrypted(t *testing.T) {
	for keyHex, encrypted := range map[string]bool{"": false, testKeyHex: true} {
		s, err := NewSealer(keyHex)
		if err != nil || s.Enabled() != encrypted {
			t.Fatalf("NewSealer(%q): enabled %v err %v", keyHex, s.Enabled(), err)
		}
		sealed, _ := s.Seal("fc-secret")
		if strings.HasPrefix(sealed, GCMPrefix) != encrypted || (encrypted && strings.Contains(sealed, "fc-secret")) {
			t.Fatalf("sealed %q", sealed)
		}
		if out, err := s.Open(sealed); err != nil || out != "fc-secret" {
			t.Fatalf("opened %q err %v", out, err)
		}
		if out, err := s.Open("fc-plain"); err != nil || out != "fc-plain" {
			t.Fatalf("an untagged value must pass through: %q err %v", out, err)
		}
	}
}

func TestAValueSealedByEarlierCodeStillOpens(t *testing.T) {
	s, _ := NewSealer(testKeyHex)
	sealed := GCMPrefix + "e114eeeafe68ad08568d25f37a6e35b63d05ad79080690ba4c453542e9a84b7f578400eb50"
	if got, err := s.Open(sealed); err != nil || got != "fc-secret" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestSealerRefusesWhatItCannotTrust(t *testing.T) {
	keyed, _ := NewSealer(testKeyHex)
	sealed, _ := keyed.Seal("fc-secret")
	plain, _ := NewSealer("")
	otherKey, _ := NewSealer(strings.Repeat("ff", 32))
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
		if _, err := c.sealer.Open(c.stored); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	for _, bad := range []string{"xyz", "00112233"} {
		if _, err := NewSealer(bad); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
}
