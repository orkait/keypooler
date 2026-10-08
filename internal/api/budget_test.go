package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"
	"github.com/orkait/keypooler/internal/writeback"
	"github.com/rs/zerolog"
)

type budgetStore struct {
	consumerDB
	budget  *db.Budget
	updated bool
	spent   float64
}

func (b *budgetStore) GetAllKeys(context.Context) ([]*db.Key, error) {
	day := 1
	return []*db.Key{
		{ID: "metered", KeyValue: "v", TierID: "anthropic", IsActive: true, Budget: &db.Budget{Amount: 200, Unit: keypool.UnitUSD, ResetDay: &day}},
		{ID: "elsewhere", KeyValue: "v", TierID: "firecrawl", IsActive: true},
	}, nil
}

func (*budgetStore) TierFeaturesByTier(context.Context) (map[string][]*db.TierFeature, error) {
	return map[string][]*db.TierFeature{
		"anthropic": {{TierID: "anthropic", Feature: "anthropic", RateLimit: 10, WindowSeconds: 60}},
		"firecrawl": {{TierID: "firecrawl", Feature: "firecrawl_scrape", RateLimit: 10, WindowSeconds: 60}},
	}, nil
}

func (*budgetStore) KeySecretsByKey(context.Context) (map[string][]*db.KeySecret, error) {
	return nil, nil
}

func (b *budgetStore) GetConsumerScopes(context.Context, string) ([]string, error) {
	return []string{"anthropic"}, nil
}

func (b *budgetStore) RecordSpend(_ context.Context, e *db.SpendEvent, _ *time.Time) (float64, bool, error) {
	b.spent += e.Amount
	return b.spent, false, nil
}

func (b *budgetStore) UpdateKeyBudget(_ context.Context, id string, budget *db.Budget) error {
	if id != "metered" {
		return db.ErrKeyNotFound
	}
	b.budget, b.updated = budget, true
	return nil
}

func budgetServer(t *testing.T) (*budgetStore, http.Handler) {
	t.Helper()
	store := &budgetStore{consumerDB: consumerDB{active: true}}
	sealer, _ := crypto.NewSealer("")
	pool, err := keypool.NewManager(store, sealer, writeback.New(nil, zerolog.Nop()), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	s := authServer(&store.consumerDB)
	s.DB, s.Pool, s.Sealer, s.Usage = store, pool, sealer, writeback.New(nil, zerolog.Nop())
	return store, NewRouter(s)
}

func call(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(headerAuthorization, bearer(token))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestASpendReportAnswersWhatTheKeyHasLeftOrWhyNot(t *testing.T) {
	cases := map[string]struct {
		key    string
		body   string
		status int
	}{
		"counted":            {"metered", `{"amount":12.5,"unit":"usd","request_id":"r1"}`, http.StatusOK},
		"no request id":      {"metered", `{"amount":12.5,"unit":"usd"}`, http.StatusBadRequest},
		"nothing spent":      {"metered", `{"amount":0,"unit":"usd","request_id":"r1"}`, http.StatusBadRequest},
		"unknown unit":       {"metered", `{"amount":1,"unit":"eur","request_id":"r1"}`, http.StatusBadRequest},
		"not json":           {"metered", `{`, http.StatusBadRequest},
		"wrong unit":         {"metered", `{"amount":1,"unit":"credits","request_id":"r1"}`, http.StatusConflict},
		"unknown key":        {"missing", `{"amount":1,"unit":"usd","request_id":"r1"}`, http.StatusNotFound},
		"outside your scope": {"elsewhere", `{"amount":1,"unit":"usd","request_id":"r1"}`, http.StatusForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, h := budgetServer(t)
			w := call(h, http.MethodPost, "/key/"+c.key+"/spend", consumerToken, c.body)
			if w.Code != c.status {
				t.Fatalf("status %d, want %d: %s", w.Code, c.status, w.Body)
			}
			if c.status != http.StatusOK {
				return
			}
			var got spendRecorded
			_ = json.Unmarshal(w.Body.Bytes(), &got)
			if got.Spent != 12.5 || got.Remaining == nil || *got.Remaining != 187.5 || got.Budget.Amount != 200 || got.ResetsAt == nil {
				t.Fatalf("answered %s", w.Body)
			}
		})
	}
}

func TestAKeyAtItsBudgetIsNoLongerDrawn(t *testing.T) {
	_, h := budgetServer(t)
	if w := call(h, http.MethodGet, "/key?feature=anthropic", consumerToken, ""); w.Code != http.StatusOK {
		t.Fatalf("before: %d", w.Code)
	}
	if w := call(h, http.MethodPost, "/key/metered/spend", consumerToken, `{"amount":200,"unit":"usd","request_id":"r1"}`); w.Code != http.StatusOK {
		t.Fatalf("spend: %d %s", w.Code, w.Body)
	}
	if w := call(h, http.MethodGet, "/key?feature=anthropic", consumerToken, ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after: %d, want 429", w.Code)
	}
}

func TestABudgetIsSetClearedOrRefused(t *testing.T) {
	cases := map[string]struct {
		key     string
		body    string
		status  int
		cleared bool
	}{
		"set":           {"metered", `{"budget":{"amount":200,"unit":"usd","reset_day":1}}`, http.StatusOK, false},
		"cleared":       {"metered", `{"budget":null}`, http.StatusOK, true},
		"no budget":     {"metered", `{}`, http.StatusBadRequest, false},
		"bad reset day": {"metered", `{"budget":{"amount":200,"unit":"usd","reset_day":31}}`, http.StatusBadRequest, false},
		"unknown key":   {"missing", `{"budget":null}`, http.StatusNotFound, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			store, h := budgetServer(t)
			w := call(h, http.MethodPatch, "/admin/keys/"+c.key, adminToken, c.body)
			if w.Code != c.status {
				t.Fatalf("status %d, want %d: %s", w.Code, c.status, w.Body)
			}
			if c.status == http.StatusOK && (!store.updated || (store.budget == nil) != c.cleared) {
				t.Fatalf("stored %+v", store.budget)
			}
		})
	}
}
