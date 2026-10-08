package keypool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

// fakeStore is the database as the pool and its writeback see it: keys and their
// tiers' features to load, and counts of what is read and written back.
type fakeStore struct {
	mu       sync.Mutex
	keys     []*db.Key
	features map[string][]*db.TierFeature
	reads    int
	inc      int
	reset    int
}

func (f *fakeStore) GetAllKeys(context.Context) ([]*db.Key, error) {
	f.reads++
	return f.keys, nil
}

func (f *fakeStore) TierFeaturesByTier(context.Context) (map[string][]*db.TierFeature, error) {
	f.reads++
	return f.features, nil
}

func (f *fakeStore) KeySecretsByKey(context.Context) (map[string][]*db.KeySecret, error) {
	f.reads++
	return nil, nil
}

func (f *fakeStore) SetKeyExhausted(context.Context, string, time.Time) error { return nil }

func (f *fakeStore) RecordUsageEvents(context.Context, []*db.UsageEvent) error { return nil }

func (f *fakeStore) AddUsage(_ context.Context, _ string, count int) error {
	f.mu.Lock()
	f.inc += count
	f.mu.Unlock()
	return nil
}

func (f *fakeStore) ResetUsageWindow(context.Context, string, time.Time, int) error {
	f.mu.Lock()
	f.reset++
	f.mu.Unlock()
	return nil
}

func draw(m *Manager, feature string) *Served {
	key, _ := m.GetKeyForFeature(feature, nil)
	return key
}

func TestAReloadRefreshesAKeysFieldsAndKeepsItsRateWindow(t *testing.T) {
	store := &fakeStore{
		keys: []*db.Key{{ID: "k", Name: "before", TierID: "t", IsActive: true}},
		features: map[string][]*db.TierFeature{
			"t": {{TierID: "t", Feature: "chat", RateLimit: 1, WindowSeconds: 60}},
		},
	}
	m := &Manager{rr: NewRoundRobin(), dbAdap: store, logger: zerolog.Nop()}
	if err := m.ReloadKeys(); err != nil {
		t.Fatal(err)
	}
	if draw(m, "chat") == nil {
		t.Fatal("first draw within the rate limit must serve")
	}

	store.keys = []*db.Key{{ID: "k", Name: "after", TierID: "t", IsActive: true}}
	if err := m.ReloadKeys(); err != nil {
		t.Fatal(err)
	}
	if draw(m, "chat") != nil {
		t.Fatal("a reload must not reset the rate window: the second draw is over the limit")
	}
	if got := m.GetHealthStatus()[0].Name; got != "after" {
		t.Fatalf("name after reload %q, want %q", got, "after")
	}
	if m.keys[0].Secrets == nil {
		t.Fatal("a key without secrets must load an empty map, which a draw answers as {}")
	}
}

func TestADrawIsNotTornByAConcurrentReload(t *testing.T) {
	store := &fakeStore{
		keys:     []*db.Key{{ID: "k", KeyValue: "v", TierID: "t", IsActive: true}},
		features: map[string][]*db.TierFeature{"t": {{TierID: "t", Feature: "chat", RateLimit: 1 << 30, WindowSeconds: 60}}},
	}
	m := &Manager{rr: NewRoundRobin(), dbAdap: store, logger: zerolog.Nop()}
	if err := m.ReloadKeys(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			if k := draw(m, "chat"); k == nil || k.KeyValue != "v" || k.Metadata != nil || k.Secrets == nil {
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
			features: map[string][]*db.TierFeature{"t": {{TierID: "t", Feature: "f", RateLimit: 10, WindowSeconds: 60}}},
		},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(store.release)
	m := &Manager{rr: NewRoundRobin(), dbAdap: store, logger: zerolog.Nop()}
	if err := m.ReloadKeys(); err != nil {
		t.Fatal(err)
	}
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
			store := &fakeStore{
				keys:     []*db.Key{{ID: "k", TierID: "t", IsActive: true, UsageLimit: &limit, UsageWindowSeconds: windowSeconds}},
				features: map[string][]*db.TierFeature{"t": {{TierID: "t", Feature: "chat", RateLimit: 10, WindowSeconds: 60}}},
			}
			m := &Manager{rr: NewRoundRobin(), dbAdap: store, usage: writeback.New(store, zerolog.Nop()), logger: zerolog.Nop()}
			if err := m.ReloadKeys(); err != nil {
				t.Fatal(err)
			}
			if draw(m, "chat") == nil {
				t.Fatal("first serve refused")
			}
			if err := m.ReloadKeys(); err != nil {
				t.Fatal(err)
			}
			if draw(m, "chat") == nil {
				t.Fatal("second serve refused")
			}
			if draw(m, "chat") != nil {
				t.Fatal("the reload forgot an unflushed serve: the key served past its limit")
			}
		})
	}
}

func TestAReloadReadsKeysFeaturesAndSecretsOnceHoweverManyKeys(t *testing.T) {
	store := &fakeStore{features: map[string][]*db.TierFeature{
		"served": {{TierID: "served", Feature: "chat", RateLimit: 10, WindowSeconds: 60}},
	}}
	for i := range 50 {
		store.keys = append(store.keys, &db.Key{ID: fmt.Sprint(i), TierID: "served", IsActive: true})
	}
	store.keys = append(store.keys, &db.Key{ID: "orphan", TierID: "featureless", IsActive: true})
	m := &Manager{rr: NewRoundRobin(), dbAdap: store, logger: zerolog.Nop()}

	if err := m.ReloadKeys(); err != nil {
		t.Fatal(err)
	}
	if store.reads != 3 || m.PoolSize() != 50 {
		t.Fatalf("reads %d, want 3; pool %d, want 50 (the featureless tier's key skipped)", store.reads, m.PoolSize())
	}
}

// A usage-limited key under heavy concurrency must be served EXACTLY usage_limit
// times - never more (credits cannot be over-spent) - and with no data race on
// UsageCount. Run with -race.
func TestUsageLimitNoOverServeUnderConcurrency(t *testing.T) {
	limit := 5
	key := &PoolKey{
		ID:         "k1",
		IsActive:   true,
		UsageLimit: &limit,
		Features:   map[string]FeatureLimit{"f": {RateLimit: 100000, WindowSeconds: 60}},
	}
	fake := &fakeStore{}
	m := &Manager{
		keys:   []*PoolKey{key},
		rr:     NewRoundRobin(),
		dbAdap: fake,
		usage:  writeback.New(fake, zerolog.Nop()),
		logger: zerolog.Nop(),
	}

	var served int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if draw(m, "f") != nil {
				atomic.AddInt32(&served, 1)
			}
		}()
	}
	wg.Wait()

	if served != int32(limit) {
		t.Fatalf("usage over/under-served: got %d, want exactly %d", served, limit)
	}
	if fake.inc != 0 {
		t.Fatalf("a serve wrote to the database on the request path: %d", fake.inc)
	}
	m.usage.Flush(context.Background())
	if fake.inc != limit {
		t.Fatalf("usage not persisted exactly per serve: %d persisted, want %d", fake.inc, limit)
	}
}

// An expired key is never handed out.
func TestExpiredKeyNotServed(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	key := &PoolKey{
		ID:        "k1",
		IsActive:  true,
		ExpiresAt: &past,
		Features:  map[string]FeatureLimit{"f": {RateLimit: 10, WindowSeconds: 60}},
	}
	m := &Manager{keys: []*PoolKey{key}, rr: NewRoundRobin(), dbAdap: &fakeStore{}, logger: zerolog.Nop()}
	if got := draw(m, "f"); got != nil {
		t.Fatalf("expired key was served")
	}
}

// A key a consumer reported spent is skipped until its time, then serves again on
// its own; the report is what the provider's refusal becomes in the pool.
func TestExhaustedKeyIsSkippedUntilItsTime(t *testing.T) {
	spent := &PoolKey{
		ID:       "spent",
		IsActive: true,
		Features: map[string]FeatureLimit{"f": {RateLimit: 10, WindowSeconds: 60}},
	}
	fresh := &PoolKey{
		ID:       "fresh",
		IsActive: true,
		Features: map[string]FeatureLimit{"f": {RateLimit: 10, WindowSeconds: 60}},
	}
	m := &Manager{keys: []*PoolKey{spent, fresh}, rr: NewRoundRobin(), dbAdap: &fakeStore{}, logger: zerolog.Nop()}

	if !m.MarkExhausted("spent", time.Now().Add(time.Hour)) {
		t.Fatalf("known key reported unknown")
	}
	if m.MarkExhausted("nobody", time.Now()) {
		t.Fatalf("unknown key reported known")
	}
	for i := 0; i < 4; i++ {
		if got := draw(m, "f"); got == nil || got.ID != "fresh" {
			t.Fatalf("draw %d served %v, want fresh", i, got)
		}
	}

	m.MarkExhausted("spent", time.Now().Add(-time.Second))
	served := map[string]bool{}
	for i := 0; i < 4; i++ {
		if got := draw(m, "f"); got != nil {
			served[got.ID] = true
		}
	}
	if !served["spent"] {
		t.Fatalf("key stayed out of rotation after its time passed")
	}
}

// A windowed usage budget exhausts within the window, then resumes after the
// window elapses. usage_limit=2, usage_window_seconds=1: serve 2, get blocked,
// wait past 1s, serve again succeeds (window reset). The reset is persisted.
func TestUsageWindowResetResumesServing(t *testing.T) {
	limit := 2
	window := 1
	key := &PoolKey{
		ID:                 "k1",
		IsActive:           true,
		UsageLimit:         &limit,
		UsageWindowSeconds: &window,
		Features:           map[string]FeatureLimit{"f": {RateLimit: 100000, WindowSeconds: 60}},
	}
	fake := &fakeStore{}
	m := &Manager{keys: []*PoolKey{key}, rr: NewRoundRobin(), dbAdap: fake, usage: writeback.New(fake, zerolog.Nop()), logger: zerolog.Nop()}

	// First window: exactly `limit` serves, then exhausted.
	for i := 0; i < limit; i++ {
		if draw(m, "f") == nil {
			t.Fatalf("serve %d within window should succeed", i+1)
		}
	}
	if draw(m, "f") != nil {
		t.Fatalf("key should be exhausted within the window")
	}

	// Wait for the window to elapse, then the budget rolls over and serving resumes.
	time.Sleep(time.Duration(window)*time.Second + 200*time.Millisecond)

	if draw(m, "f") == nil {
		t.Fatalf("key should serve again after the usage window reset")
	}

	m.usage.Flush(context.Background())
	fake.mu.Lock()
	resets := fake.reset
	fake.mu.Unlock()
	if resets < 1 {
		t.Fatalf("window reset not persisted: ResetUsageWindow called %d times, want >= 1", resets)
	}
}
