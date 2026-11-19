package keypool

import (
	"context"
	"strings"
	"testing"

	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
)

const atRestKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

type atRestStore struct {
	keys    map[string]string
	secrets map[string]map[string]string
	writes  int
}

func (s *atRestStore) GetAllKeys(context.Context) ([]*db.Key, error) {
	var out []*db.Key
	for id, value := range s.keys {
		out = append(out, &db.Key{ID: id, KeyValue: value})
	}
	return out, nil
}

func (s *atRestStore) KeySecretsByKey(context.Context) (map[string][]*db.KeySecret, error) {
	out := map[string][]*db.KeySecret{}
	for keyID, byName := range s.secrets {
		for name, value := range byName {
			out[keyID] = append(out[keyID], &db.KeySecret{KeyID: keyID, Name: name, Value: value})
		}
	}
	return out, nil
}

func (s *atRestStore) ReplaceKeyValue(_ context.Context, keyID, value string) error {
	s.writes++
	s.keys[keyID] = value
	return nil
}

func (s *atRestStore) ReplaceKeySecretValue(_ context.Context, keyID, name, value string) error {
	s.writes++
	s.secrets[keyID][name] = value
	return nil
}

func TestEveryPlainValueIsSealedOnceAndStillOpensToTheSameSecret(t *testing.T) {
	sealer, _ := crypto.NewSealer(atRestKeyHex, "")
	already, _ := sealer.Seal("already-sealed")
	store := &atRestStore{
		keys:    map[string]string{"plain": "fc-1", "sealed": already},
		secrets: map[string]map[string]string{"plain": {"webhook": "wh-1"}},
	}
	for run, want := range []int{2, 0} {
		sealed, err := SealAtRest(context.Background(), store, sealer)
		if err != nil || sealed != want {
			t.Fatalf("run %d sealed %d err %v, want %d", run, sealed, err, want)
		}
	}
	for stored, plain := range map[string]string{
		store.keys["plain"]:               "fc-1",
		store.keys["sealed"]:              "already-sealed",
		store.secrets["plain"]["webhook"]: "wh-1",
	} {
		if !crypto.IsSealed(stored) {
			t.Errorf("%q is not sealed", stored)
		}
		if opened, err := sealer.Open(stored); err != nil || opened != plain {
			t.Errorf("opened %q err %v, want %q", opened, err, plain)
		}
	}
}

func TestARotationReSealsEveryValueUnderTheNewKeyOnce(t *testing.T) {
	newKeyHex := strings.Repeat("ab", 32)
	old, _ := crypto.NewSealer(atRestKeyHex, "")
	rotating, _ := crypto.NewSealer(newKeyHex, atRestKeyHex)
	fromOld, _ := old.Seal("fc-old")
	secretFromOld, _ := old.Seal("wh-old")
	fromNew, _ := rotating.Seal("fc-new")
	store := &atRestStore{
		keys:    map[string]string{"old": fromOld, "new": fromNew, "plain": "fc-plain"},
		secrets: map[string]map[string]string{"old": {"webhook": secretFromOld}},
	}
	for run, want := range []int{3, 0} {
		sealed, err := SealAtRest(context.Background(), store, rotating)
		if err != nil || sealed != want {
			t.Fatalf("run %d sealed %d err %v, want %d", run, sealed, err, want)
		}
	}
	newOnly, _ := crypto.NewSealer(newKeyHex, "")
	for stored, plain := range map[string]string{
		store.keys["old"]:               "fc-old",
		store.keys["new"]:               "fc-new",
		store.keys["plain"]:             "fc-plain",
		store.secrets["old"]["webhook"]: "wh-old",
	} {
		if opened, err := newOnly.Open(stored); err != nil || opened != plain {
			t.Errorf("under the new key alone opened %q err %v, want %q", opened, err, plain)
		}
	}
}

func TestAValueNeitherKeyOpensStopsTheBoot(t *testing.T) {
	stranger, _ := crypto.NewSealer(strings.Repeat("cd", 32), "")
	lost, _ := stranger.Seal("fc-lost")
	rotating, _ := crypto.NewSealer(strings.Repeat("ab", 32), atRestKeyHex)
	store := &atRestStore{keys: map[string]string{"lost": lost}, secrets: map[string]map[string]string{}}
	if _, err := SealAtRest(context.Background(), store, rotating); err == nil || store.writes != 0 {
		t.Fatalf("err %v writes %d: a value no key opens must stop the boot untouched", err, store.writes)
	}
}

func TestWithoutAnEncryptionKeyNothingIsRewritten(t *testing.T) {
	sealer, _ := crypto.NewSealer("", "")
	store := &atRestStore{keys: map[string]string{"plain": "fc-1"}, secrets: map[string]map[string]string{}}
	if sealed, err := SealAtRest(context.Background(), store, sealer); err != nil || sealed != 0 || store.writes != 0 {
		t.Fatalf("sealed %d writes %d err %v", sealed, store.writes, err)
	}
}
