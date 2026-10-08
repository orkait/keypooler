package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/config"
	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"
	"github.com/orkait/keypooler/internal/writeback"
	"github.com/rs/zerolog"
)

// keyStore answers what adding a key reads and records what it writes; any
// other call panics on the nil embedded interface.
type keyStore struct {
	db.DBAdapter
	created *db.Key
	secrets []*db.KeySecret
}

func (k *keyStore) GetTierByName(_ context.Context, name string) (*db.Tier, error) {
	if name != "known" {
		return nil, nil
	}
	return &db.Tier{ID: "tier-1", Name: name}, nil
}

func (k *keyStore) CreateKey(_ context.Context, key *db.Key, secrets []*db.KeySecret) error {
	k.created, k.secrets = key, secrets
	return nil
}

func (k *keyStore) GetAllKeys(context.Context) ([]*db.Key, error) { return nil, nil }

func (k *keyStore) TierFeaturesByTier(context.Context) (map[string][]*db.TierFeature, error) {
	return nil, nil
}

func (k *keyStore) KeySecretsByKey(context.Context) (map[string][]*db.KeySecret, error) {
	return nil, nil
}

func addKey(t *testing.T, body string) (*keyStore, *httptest.ResponseRecorder) {
	t.Helper()
	store := &keyStore{}
	sealer, _ := crypto.NewSealer("")
	pool, err := keypool.NewManager(store, sealer, writeback.New(nil, zerolog.Nop()), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	h := NewRouter(&Server{
		DB: store, Pool: pool, Sealer: sealer, Logger: zerolog.Nop(),
		Cfg: &config.Config{AdminToken: adminToken}, Auth: NewAuthCache(time.Minute),
	})
	r := httptest.NewRequest(http.MethodPost, "/admin/keys", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return store, w
}

func TestAKeyIsStoredWithItsTierLimitsAndSecretsAndEchoedWithoutThem(t *testing.T) {
	store, w := addKey(t, `{"name":"k","key":"sk-1","tier":"known","expires_at":"2027-01-01T00:00:00Z",
		"usage_limit":5,"metadata":{"account":"a"},"secrets":{"webhook":"s"}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	k := store.created
	if k.TierID != "tier-1" || k.KeyValue != "sk-1" || !k.IsActive || *k.UsageLimit != 5 ||
		!k.ExpiresAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) || k.Metadata["account"] != "a" {
		t.Fatalf("stored %+v", k)
	}
	if len(store.secrets) != 1 || store.secrets[0].KeyID != k.ID || store.secrets[0].Value != "s" {
		t.Fatalf("secrets %+v", store.secrets)
	}
	var echoed map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &echoed)
	if echoed["id"] != k.ID || echoed["secret_names"].([]any)[0] != "webhook" || echoed["key"] != nil {
		t.Fatalf("echoed %v", echoed)
	}
}

func TestAKeyThatCannotBeAddedSaysWhy(t *testing.T) {
	cases := map[string]struct {
		body   string
		status int
	}{
		"missing-key":  {`{"name":"k","tier":"known"}`, http.StatusBadRequest},
		"bad-expiry":   {`{"name":"k","key":"sk","tier":"known","expires_at":"soon"}`, http.StatusBadRequest},
		"unknown-tier": {`{"name":"k","key":"sk","tier":"nope"}`, http.StatusNotFound},
		"not-json":     {`{`, http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			store, w := addKey(t, c.body)
			if w.Code != c.status || store.created != nil {
				t.Fatalf("status %d (want %d), created %+v", w.Code, c.status, store.created)
			}
		})
	}
}
