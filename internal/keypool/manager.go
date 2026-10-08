package keypool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/util"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

// Store is what the pool reads from and writes to the database.
type Store interface {
	GetAllKeys(ctx context.Context) ([]*db.Key, error)
	TierFeaturesByTier(ctx context.Context) (map[string][]*db.TierFeature, error)
	KeySecretsByKey(ctx context.Context) (map[string][]*db.KeySecret, error)
	SetKeyExhausted(ctx context.Context, keyID string, until time.Time) error
}

// Manager owns all pool keys and selects them via round-robin.
type Manager struct {
	mu     sync.RWMutex
	keys   []*PoolKey
	rr     *RoundRobin
	dbAdap Store
	usage  *writeback.Writer
	sealer *crypto.Sealer
	logger zerolog.Logger
}

// NewManager creates a key pool manager and loads keys from the database. The
// sealer opens (decrypts where tagged) bound secrets as keys are loaded; usage
// writes go through the writeback writer, off the serve path.
func NewManager(dbAdap Store, sealer *crypto.Sealer, usage *writeback.Writer, logger zerolog.Logger) (*Manager, error) {
	m := &Manager{
		rr:     NewRoundRobin(),
		dbAdap: dbAdap,
		usage:  usage,
		sealer: sealer,
		logger: logger.With().Str("component", "keypool").Logger(),
	}

	if err := m.ReloadKeys(); err != nil {
		return nil, err
	}

	return m, nil
}

var (
	ErrOutOfScope = errors.New("no key in scope serves the feature")
	ErrExhausted  = errors.New("no key available for feature")
)

// Served is a drawn key's fields, copied under the pool lock so a reload cannot
// rewrite them while the caller reads.
type Served struct {
	ID       string
	KeyValue string
	Metadata map[string]any
	Secrets  map[string]string
}

func (m *Manager) candidates(feature string, allowedTierIDs map[string]bool) (available []*PoolKey, offered bool) {
	for _, key := range m.keys {
		if (allowedTierIDs != nil && !allowedTierIDs[key.TierID]) || !key.HasFeature(feature) {
			continue
		}
		offered = true
		if key.Available() {
			available = append(available, key)
		}
	}
	return available, offered
}

// GetKeyForFeature draws a key in an allowed tier (nil allows every tier) with
// rate and usage budget left. The rate window is tried first so a rate-blocked
// key does not consume usage.
func (m *Manager) GetKeyForFeature(feature string, allowedTierIDs map[string]bool) (*Served, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	available, offered := m.candidates(feature, allowedTierIDs)
	for len(available) > 0 {
		selected := m.rr.Select(available)
		if selected.TryRate(feature) {
			if ok, didReset, windowStart := selected.TryConsumeUsage(); ok {
				if selected.UsageLimit != nil {
					m.persistUsage(selected, didReset, windowStart)
				}
				return &Served{ID: selected.ID, KeyValue: selected.KeyValue, Metadata: selected.Metadata, Secrets: selected.Secrets}, nil
			}
		}
		available = removeKey(available, selected)
	}
	if !offered {
		return nil, ErrOutOfScope
	}
	return nil, ErrExhausted
}

// persistUsage queues the cumulative usage change (the in-memory mutation already
// happened atomically in TryConsumeUsage) so a restart resumes the count. A rolled
// over window (didReset) queues the reset instead of an increment. The write lands
// within a flush period; a crash loses at most that period's counts.
// Single-replica assumption: under multiple replicas the in-memory count is
// per-replica; only the DB count is authoritative.
func (m *Manager) persistUsage(key *PoolKey, didReset bool, windowStart time.Time) {
	if didReset {
		m.usage.WindowReset(key.ID, windowStart)
		return
	}
	m.usage.Served(key.ID)
}

// ReloadKeys reads all keys from the database and rebuilds the pool.
// Preserves runtime state (rate counters) for existing keys.
func (m *Manager) ReloadKeys() error {
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
	defer cancel()

	// Three reads however many keys there are: keys, every tier's features, every
	// key's secrets.
	dbKeys, err := m.dbAdap.GetAllKeys(ctx)
	if err != nil {
		return err
	}
	byTier, err := m.dbAdap.TierFeaturesByTier(ctx)
	if err != nil {
		return err
	}
	byKey, err := m.dbAdap.KeySecretsByKey(ctx)
	if err != nil {
		return err
	}
	tierFeatures := featureLimits(byTier)

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
			m.logger.Warn().Str("key_id", k.ID).Str("tier_id", k.TierID).
				Msg("key skipped from pool: tier has no features")
			continue
		}

		// A key already in the pool keeps its object, and with it its runtime state.
		key, ok := existing[k.ID]
		if !ok {
			key = &PoolKey{UsageCount: k.UsageCount, UsageWindowStart: k.UsageWindowStart}
		}
		key.load(k, features, m.openSecrets(k.ID, byKey[k.ID]))
		newKeys = append(newKeys, key)
	}

	m.keys = newKeys
	m.logger.Debug().Int("key_count", len(m.keys)).Msg("key pool reloaded")
	return nil
}

// featureLimits indexes each tier's features by name: tierID -> feature -> limit.
func featureLimits(byTier map[string][]*db.TierFeature) map[string]map[string]FeatureLimit {
	limits := make(map[string]map[string]FeatureLimit, len(byTier))
	for tierID, features := range byTier {
		byName := make(map[string]FeatureLimit, len(features))
		for _, f := range features {
			byName[f.Feature] = FeatureLimit{RateLimit: f.RateLimit, WindowSeconds: f.WindowSeconds}
		}
		limits[tierID] = byName
	}
	return limits
}

// openSecrets opens a key's bound secrets via the sealer (values tagged as
// encrypted are decrypted, plaintext values pass through) into a name->value map.
// Open failures are logged without the value and skipped.
func (m *Manager) openSecrets(keyID string, rows []*db.KeySecret) map[string]string {
	secrets := make(map[string]string, len(rows))
	for _, s := range rows {
		plain, derr := m.sealer.Open(s.Value)
		if derr != nil {
			m.logger.Error().Err(derr).Str("key_id", keyID).Str("secret", s.Name).Msg("failed to open secret")
			continue
		}
		secrets[s.Name] = plain
	}
	return secrets
}

func (m *Manager) find(id string) *PoolKey {
	for _, key := range m.keys {
		if key.ID == id {
			return key
		}
	}
	return nil
}

// TierOf reports the tier a pooled key belongs to, and false when the key is unknown.
func (m *Manager) TierOf(id string) (tierID string, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if key := m.find(id); key != nil {
		return key.TierID, true
	}
	return "", false
}

// MarkExhausted takes a key out of rotation until `until`, then persists it
// outside the pool lock so draws never wait on the database.
func (m *Manager) MarkExhausted(id string, until time.Time) bool {
	m.mu.Lock()
	key := m.find(id)
	if key != nil {
		key.ExhaustedUntil = &until
	}
	m.mu.Unlock()
	if key == nil {
		return false
	}
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
	defer cancel()
	if err := m.dbAdap.SetKeyExhausted(ctx, id, until); err != nil {
		m.logger.Error().Err(err).Str("key_id", id).Msg("failed to persist exhausted_until")
	}
	return true
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
		secretNames := make([]string, 0, len(key.Secrets))
		for name := range key.Secrets {
			secretNames = append(secretNames, name)
		}
		statuses[i] = KeyHealth{
			ID:             key.ID,
			Name:           key.Name,
			TierID:         key.TierID,
			IsActive:       key.IsActive,
			ExpiresAt:      key.ExpiresAt,
			ExhaustedUntil: key.ExhaustedUntil,
			UsageLimit:     key.UsageLimit,
			UsageCount:     key.UsageSnapshot(),
			Metadata:       key.Metadata,
			SecretNames:    secretNames,
			Usage:          key.RateUsage(),
		}
	}
	return statuses
}

// KeyHealth is a read-only snapshot of a key's health. It never carries
// decrypted secret values, only their names.
type KeyHealth struct {
	ID             string
	Name           string
	TierID         string
	IsActive       bool
	ExpiresAt      *time.Time
	ExhaustedUntil *time.Time
	UsageLimit     *int
	UsageCount     int
	Metadata       map[string]any
	SecretNames    []string
	Usage          map[string]RateInfo
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
