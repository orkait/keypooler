package keypool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"key-pool-system/internal/db"

	"github.com/rs/zerolog"
)

// noopDB is a no-op DBAdapter; IncrementUsage and ResetUsageWindow are exercised
// (counted) by the usage/window tests.
type noopDB struct {
	mu    sync.Mutex
	inc   int
	reset int
}

func (n *noopDB) Close() error                                                       { return nil }
func (n *noopDB) CreateTier(context.Context, *db.Tier) error                         { return nil }
func (n *noopDB) GetTier(context.Context, string) (*db.Tier, error)                  { return nil, nil }
func (n *noopDB) GetTierByName(context.Context, string) (*db.Tier, error)            { return nil, nil }
func (n *noopDB) GetAllTiers(context.Context) ([]*db.Tier, error)                    { return nil, nil }
func (n *noopDB) DeleteTier(context.Context, string) error                           { return nil }
func (n *noopDB) UpdateTierDescription(context.Context, string, string) error        { return nil }
func (n *noopDB) SetTierFeatures(context.Context, string, []*db.TierFeature) error   { return nil }
func (n *noopDB) GetTierFeatures(context.Context, string) ([]*db.TierFeature, error) { return nil, nil }
func (n *noopDB) CreateKey(context.Context, *db.Key) error                           { return nil }
func (n *noopDB) GetKey(context.Context, string) (*db.Key, error)                    { return nil, nil }
func (n *noopDB) GetAllKeys(context.Context) ([]*db.Key, error)                      { return nil, nil }
func (n *noopDB) GetKeysByTier(context.Context, string) ([]*db.Key, error)           { return nil, nil }
func (n *noopDB) DeleteKey(context.Context, string) error                            { return nil }
func (n *noopDB) SetKeyActive(context.Context, string, bool) error                   { return nil }
func (n *noopDB) GetKeySecrets(context.Context, string) ([]*db.KeySecret, error)     { return nil, nil }
func (n *noopDB) SetKeySecrets(context.Context, string, []*db.KeySecret) error       { return nil }
func (n *noopDB) CreateConsumer(context.Context, *db.Consumer) error                 { return nil }
func (n *noopDB) GetConsumerByTokenHash(context.Context, string) (*db.Consumer, error) {
	return nil, nil
}
func (n *noopDB) GetAllConsumers(context.Context) ([]*db.Consumer, error)        { return nil, nil }
func (n *noopDB) DeleteConsumer(context.Context, string) error                   { return nil }
func (n *noopDB) AddConsumerScope(context.Context, string, string) error         { return nil }
func (n *noopDB) GetConsumerScopes(context.Context, string) ([]string, error)    { return nil, nil }
func (n *noopDB) RecordUsageEvent(context.Context, string, string, string) error { return nil }
func (n *noopDB) ListUsageEvents(context.Context, int) ([]*db.UsageEvent, error) {
	return nil, nil
}
func (n *noopDB) IncrementUsage(context.Context, string) error {
	n.mu.Lock()
	n.inc++
	n.mu.Unlock()
	return nil
}
func (n *noopDB) ResetUsageWindow(context.Context, string, time.Time) error {
	n.mu.Lock()
	n.reset++
	n.mu.Unlock()
	return nil
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
	fake := &noopDB{}
	m := &Manager{
		keys:   []*PoolKey{key},
		rr:     NewRoundRobin(),
		dbAdap: fake,
		logger: zerolog.Nop(),
	}

	var served int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.GetKeyForFeature("f", nil) != nil {
				atomic.AddInt32(&served, 1)
			}
		}()
	}
	wg.Wait()

	if served != int32(limit) {
		t.Fatalf("usage over/under-served: got %d, want exactly %d", served, limit)
	}
	if fake.inc != limit {
		t.Fatalf("usage not persisted exactly per serve: IncrementUsage called %d times, want %d", fake.inc, limit)
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
	m := &Manager{keys: []*PoolKey{key}, rr: NewRoundRobin(), dbAdap: &noopDB{}, logger: zerolog.Nop()}
	if got := m.GetKeyForFeature("f", nil); got != nil {
		t.Fatalf("expired key was served")
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
	fake := &noopDB{}
	m := &Manager{keys: []*PoolKey{key}, rr: NewRoundRobin(), dbAdap: fake, logger: zerolog.Nop()}

	// First window: exactly `limit` serves, then exhausted.
	for i := 0; i < limit; i++ {
		if m.GetKeyForFeature("f", nil) == nil {
			t.Fatalf("serve %d within window should succeed", i+1)
		}
	}
	if m.GetKeyForFeature("f", nil) != nil {
		t.Fatalf("key should be exhausted within the window")
	}

	// Wait for the window to elapse, then the budget rolls over and serving resumes.
	time.Sleep(time.Duration(window)*time.Second + 200*time.Millisecond)

	if m.GetKeyForFeature("f", nil) == nil {
		t.Fatalf("key should serve again after the usage window reset")
	}

	fake.mu.Lock()
	resets := fake.reset
	fake.mu.Unlock()
	if resets < 1 {
		t.Fatalf("window reset not persisted: ResetUsageWindow called %d times, want >= 1", resets)
	}
}
