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

// noopDB is a no-op DBAdapter; only IncrementUsage is exercised (counted).
type noopDB struct {
	mu  sync.Mutex
	inc int
}

func (n *noopDB) Close() error                                                       { return nil }
func (n *noopDB) CreateTier(context.Context, *db.Tier) error                         { return nil }
func (n *noopDB) GetTier(context.Context, string) (*db.Tier, error)                  { return nil, nil }
func (n *noopDB) GetTierByName(context.Context, string) (*db.Tier, error)            { return nil, nil }
func (n *noopDB) GetAllTiers(context.Context) ([]*db.Tier, error)                    { return nil, nil }
func (n *noopDB) DeleteTier(context.Context, string) error                           { return nil }
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
func (n *noopDB) IncrementUsage(context.Context, string) error {
	n.mu.Lock()
	n.inc++
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
			if m.GetKeyForFeature("f") != nil {
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
	if got := m.GetKeyForFeature("f"); got != nil {
		t.Fatalf("expired key was served")
	}
}
