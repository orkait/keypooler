package keypool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

type fakeStore struct {
	mu       sync.Mutex
	keys     []*db.Key
	features map[string][]*db.TierFeature
	inc      int
	reset    int
	spent    float64
	spends   int
	period   *time.Time
}

func (f *fakeStore) RecordSpend(_ context.Context, _ *db.SpendEvent, period *time.Time) (float64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spends++
	f.period = period
	return f.spent, false, nil
}

func (f *fakeStore) GetAllKeys(context.Context) ([]*db.Key, error) { return f.keys, nil }

func (f *fakeStore) TierFeaturesByTier(context.Context) (map[string][]*db.TierFeature, error) {
	return f.features, nil
}

func (f *fakeStore) KeySecretsByKey(context.Context) (map[string][]*db.KeySecret, error) {
	return nil, nil
}

func (f *fakeStore) SetKeyExhausted(context.Context, string, time.Time) error { return nil }

func (f *fakeStore) RecordUsageEvents(context.Context, []*db.UsageEvent) error { return nil }

func (f *fakeStore) AddUsage(_ context.Context, _ string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inc += n
	return nil
}

func (f *fakeStore) ResetUsageWindow(context.Context, string, time.Time, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reset++
	return nil
}

func tier(feature string, rate int) map[string][]*db.TierFeature {
	return map[string][]*db.TierFeature{"t": {{TierID: "t", Feature: feature, RateLimit: rate, WindowSeconds: 60}}}
}

func loaded(t *testing.T, store Store) *Manager {
	t.Helper()
	m, err := NewManager(store, nil, writeback.New(nil, zerolog.Nop()), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func pooled(keys ...*PoolKey) (*Manager, *fakeStore) {
	store := &fakeStore{}
	return &Manager{keys: keys, dbAdap: store, usage: writeback.New(store, zerolog.Nop()), logger: zerolog.Nop()}, store
}

func draw(m *Manager, feature string) *Served {
	key, _ := m.GetKeyForFeature(feature, nil)
	return key
}

func TestAReloadRefreshesFieldsKeepsTheRateWindowAndSkipsFeaturelessTiers(t *testing.T) {
	store := &fakeStore{
		keys:     []*db.Key{{ID: "k", Name: "before", TierID: "t", IsActive: true}, {ID: "orphan", TierID: "none", IsActive: true}},
		features: tier("chat", 1),
	}
	m := loaded(t, store)
	if m.PoolSize() != 1 || draw(m, "chat") == nil {
		t.Fatalf("pool %d, want the featureless tier's key skipped and k served", m.PoolSize())
	}
	store.keys = []*db.Key{{ID: "k", Name: "after", TierID: "t", IsActive: true}}
	if err := m.ReloadKeys(); err != nil {
		t.Fatal(err)
	}
	if draw(m, "chat") != nil {
		t.Fatal("a reload must not reset the rate window")
	}
	if got := m.GetHealthStatus()[0].Name; got != "after" {
		t.Fatalf("name after reload %q, want after", got)
	}
}

func TestADrawIsNotTornByAConcurrentReload(t *testing.T) {
	m := loaded(t, &fakeStore{keys: []*db.Key{{ID: "k", KeyValue: "v", TierID: "t", IsActive: true}}, features: tier("chat", 1<<30)})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			if k := draw(m, "chat"); k == nil || k.KeyValue != "v" || k.Secrets == nil {
				t.Error("draw lost its fields")
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = m.ReloadKeys()
		}
	}()
	wg.Wait()
}

type slowExhaust struct {
	*fakeStore
	entered, release chan struct{}
}

func (s slowExhaust) SetKeyExhausted(context.Context, string, time.Time) error {
	close(s.entered)
	<-s.release
	return nil
}

func TestReportingAKeyExhaustedDoesNotHoldUpDraws(t *testing.T) {
	store := slowExhaust{
		fakeStore: &fakeStore{
			keys:     []*db.Key{{ID: "spent", TierID: "t", IsActive: true}, {ID: "fresh", TierID: "t", IsActive: true}},
			features: tier("f", 10),
		},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(store.release)
	m := loaded(t, store)
	go m.MarkExhausted("spent", time.Now().Add(time.Hour))
	<-store.entered

	drawn := make(chan *Served, 1)
	go func() { drawn <- draw(m, "f") }()
	select {
	case got := <-drawn:
		if got == nil || got.ID != "fresh" {
			t.Fatalf("drew %v, want fresh", got)
		}
	case <-time.After(time.Second):
		t.Fatal("a draw waited on the exhausted write to the database")
	}
}

func TestAReloadKeepsServesTheDatabaseHasNotSeenYet(t *testing.T) {
	window := 3600
	for name, windowSeconds := range map[string]*int{"lifetime": nil, "windowed": &window} {
		t.Run(name, func(t *testing.T) {
			limit := 2
			m := loaded(t, &fakeStore{
				keys:     []*db.Key{{ID: "k", TierID: "t", IsActive: true, UsageLimit: &limit, UsageWindowSeconds: windowSeconds}},
				features: tier("chat", 10),
			})
			draw(m, "chat")
			if err := m.ReloadKeys(); err != nil {
				t.Fatal(err)
			}
			if draw(m, "chat") == nil || draw(m, "chat") != nil {
				t.Fatal("the reload forgot an unflushed serve")
			}
		})
	}
}

func TestUsageLimitNoOverServeUnderConcurrency(t *testing.T) {
	limit := 5
	m, store := pooled(&PoolKey{ID: "k", IsActive: true, UsageLimit: &limit, Features: map[string]FeatureLimit{"f": {RateLimit: 100000}}})
	var served atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if draw(m, "f") != nil {
				served.Add(1)
			}
		}()
	}
	wg.Wait()
	if served.Load() != int32(limit) || store.inc != 0 {
		t.Fatalf("served %d (want %d), written on the request path %d", served.Load(), limit, store.inc)
	}
	m.usage.Flush(context.Background())
	if store.inc != limit {
		t.Fatalf("persisted %d, want %d", store.inc, limit)
	}
}

func TestAKeyIsSkippedWhileExpiredOrExhaustedAndServesAgainAfter(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	features := map[string]FeatureLimit{"f": {RateLimit: 10}}
	m, _ := pooled(
		&PoolKey{ID: "expired", IsActive: true, ExpiresAt: &past, Features: features},
		&PoolKey{ID: "spent", IsActive: true, Features: features},
		&PoolKey{ID: "fresh", IsActive: true, Features: features},
	)
	if !m.MarkExhausted("spent", time.Now().Add(time.Hour)) || m.MarkExhausted("nobody", time.Now()) {
		t.Fatal("MarkExhausted must know exactly the pooled keys")
	}
	for range 4 {
		if got := draw(m, "f"); got == nil || got.ID != "fresh" {
			t.Fatalf("served %v, want fresh", got)
		}
	}
	m.MarkExhausted("spent", time.Now().Add(-time.Second))
	served := map[string]bool{}
	for range 4 {
		served[draw(m, "f").ID] = true
	}
	if !served["spent"] || served["expired"] {
		t.Fatalf("served %v", served)
	}
}

func TestAnElapsedUsageWindowRollsOverAndThatIsPersisted(t *testing.T) {
	limit, window := 1, 60
	key := &PoolKey{ID: "k", IsActive: true, UsageLimit: &limit, UsageWindowSeconds: &window, Features: map[string]FeatureLimit{"f": {RateLimit: 100}}}
	m, store := pooled(key)
	if draw(m, "f") == nil || draw(m, "f") != nil {
		t.Fatal("want one serve, then the window's budget spent")
	}
	elapsed := time.Now().Add(-time.Duration(window) * time.Second)
	key.UsageWindowStart = &elapsed
	if draw(m, "f") == nil {
		t.Fatal("the key must serve again once its window elapsed")
	}
	m.usage.Flush(context.Background())
	if store.reset != 1 {
		t.Fatalf("window resets persisted %d, want 1", store.reset)
	}
}

func TestASyncSeesEveryActiveKeyOfAFeatureAndItsBalanceIsListed(t *testing.T) {
	later := time.Now().Add(time.Hour)
	features := map[string]FeatureLimit{"scrape": {RateLimit: 10, WindowSeconds: 60}}
	m, _ := pooled(
		&PoolKey{ID: "serving", KeyValue: "v1", IsActive: true, Features: features},
		&PoolKey{ID: "benched", KeyValue: "v2", IsActive: true, ExhaustedUntil: &later, Features: features},
		&PoolKey{ID: "off", KeyValue: "v3", Features: features},
		&PoolKey{ID: "other", KeyValue: "v4", IsActive: true, Features: map[string]FeatureLimit{"search": {RateLimit: 10, WindowSeconds: 60}}},
	)
	held := m.Holding("scrape")
	if len(held) != 2 || held[0] != (Held{ID: "serving", KeyValue: "v1"}) || held[1].ID != "benched" {
		t.Fatalf("held %+v", held)
	}
	m.SetBalance("serving", Balance{Used: 4, Limit: 1000, Unit: UnitCredits})
	for _, h := range m.GetHealthStatus() {
		if (h.Balance != nil) != (h.ID == "serving") || (h.Balance != nil && h.Balance.Used != 4) {
			t.Fatalf("%s balance %+v", h.ID, h.Balance)
		}
	}
}
