package keypool

import (
	"context"
	"sync"

	"key-pool-system/internal/db"
	"key-pool-system/internal/util"
	"github.com/rs/zerolog"
)

// Manager owns all pool keys and selects them via round-robin.
type Manager struct {
	mu     sync.RWMutex
	keys   []*PoolKey
	rr     *RoundRobin
	dbAdap db.DBAdapter
	logger zerolog.Logger
}

// NewManager creates a key pool manager and loads keys from the database.
func NewManager(dbAdap db.DBAdapter, logger zerolog.Logger) (*Manager, error) {
	m := &Manager{
		rr:     NewRoundRobin(),
		dbAdap: dbAdap,
		logger: logger.With().Str("component", "keypool").Logger(),
	}

	if err := m.ReloadKeys(); err != nil {
		return nil, err
	}

	return m, nil
}

// GetKeyForFeature selects an available key that supports the given feature
// and has rate budget remaining.
func (m *Manager) GetKeyForFeature(feature string) *PoolKey {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Filter to keys that support this feature, are active, and have rate
	var available []*PoolKey
	for _, key := range m.keys {
		if !key.IsActive {
			continue
		}
		if !key.HasFeature(feature) {
			continue
		}
		available = append(available, key)
	}

	if len(available) == 0 {
		return nil
	}

	// Try each key via round-robin until one has rate budget
	for attempts := 0; attempts < len(available); attempts++ {
		selected := m.rr.Select(available)
		if selected == nil {
			return nil
		}

		if selected.TryRate(feature) {
			return selected
		}

		// Rate exhausted, remove from candidates and try again
		available = removeKey(available, selected)
		if len(available) == 0 {
			return nil
		}
	}

	return nil
}

// ReloadKeys reads all keys from the database and rebuilds the pool.
// Preserves runtime state (rate counters) for existing keys.
func (m *Manager) ReloadKeys() error {
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
	defer cancel()

	dbKeys, err := m.dbAdap.GetAllKeys(ctx)
	if err != nil {
		return err
	}

	// Load tier features for each key
	tierFeatures := make(map[string]map[string]int) // tierID -> feature -> rate
	for _, k := range dbKeys {
		if _, ok := tierFeatures[k.TierID]; ok {
			continue
		}
		features, err := m.dbAdap.GetTierFeatures(ctx, k.TierID)
		if err != nil {
			m.logger.Error().Err(err).Str("tier_id", k.TierID).Msg("failed to load tier features")
			continue
		}
		fm := make(map[string]int, len(features))
		for _, f := range features {
			fm[f.Feature] = f.RatePerMinute
		}
		tierFeatures[k.TierID] = fm
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing := make(map[string]*PoolKey, len(m.keys))
	for _, key := range m.keys {
		existing[key.ID] = key
	}

	newKeys := make([]*PoolKey, 0, len(dbKeys))
	for _, k := range dbKeys {
		features := tierFeatures[k.TierID]
		if features == nil {
			continue
		}

		if old, ok := existing[k.ID]; ok {
			// Preserve runtime rate state, update DB fields
			old.Name = k.Name
			old.KeyEncrypted = k.KeyEncrypted
			old.TierID = k.TierID
			old.IsActive = k.IsActive
			old.Features = features
			newKeys = append(newKeys, old)
		} else {
			newKeys = append(newKeys, &PoolKey{
				ID:           k.ID,
				Name:         k.Name,
				KeyEncrypted: k.KeyEncrypted,
				TierID:       k.TierID,
				IsActive:     k.IsActive,
				Features:     features,
			})
		}
	}

	m.keys = newKeys
	m.logger.Debug().Int("key_count", len(m.keys)).Msg("key pool reloaded")
	return nil
}

// PoolSize returns the number of keys in the pool.
func (m *Manager) PoolSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.keys)
}

// GetHealthStatus returns a snapshot of all keys' current state.
func (m *Manager) GetHealthStatus() []KeyHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]KeyHealth, len(m.keys))
	for i, key := range m.keys {
		statuses[i] = KeyHealth{
			ID:       key.ID,
			Name:     key.Name,
			TierID:   key.TierID,
			IsActive: key.IsActive,
			Usage:    key.RateUsage(),
		}
	}
	return statuses
}

// KeyHealth is a read-only snapshot of a key's health.
type KeyHealth struct {
	ID       string
	Name     string
	TierID   string
	IsActive bool
	Usage    map[string]RateInfo
}

func removeKey(keys []*PoolKey, target *PoolKey) []*PoolKey {
	result := make([]*PoolKey, 0, len(keys)-1)
	for _, k := range keys {
		if k != target {
			result = append(result, k)
		}
	}
	return result
}
