package keypool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

const dbTimeout = 5 * time.Second

var (
	ErrOutOfScope = errors.New("no key in scope serves the feature")
	ErrExhausted  = errors.New("no key available for feature")
)

type Store interface {
	GetAllKeys(ctx context.Context) ([]*db.Key, error)
	TierFeaturesByTier(ctx context.Context) (map[string][]*db.TierFeature, error)
	KeySecretsByKey(ctx context.Context) (map[string][]*db.KeySecret, error)
	SetKeyExhausted(ctx context.Context, keyID string, until time.Time) error
}

type Manager struct {
	mu     sync.RWMutex
	keys   []*PoolKey
	next   atomic.Uint64
	dbAdap Store
	usage  *writeback.Writer
	sealer *crypto.Sealer
	logger zerolog.Logger
}

type Served struct {
	ID       string
	KeyValue string
	Metadata map[string]any
	Secrets  map[string]string
}

func NewManager(dbAdap Store, sealer *crypto.Sealer, usage *writeback.Writer, logger zerolog.Logger) (*Manager, error) {
	m := &Manager{
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

func (m *Manager) GetKeyForFeature(feature string, allowedTierIDs map[string]bool) (*Served, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var available []*PoolKey
	offered := false
	for _, key := range m.keys {
		if (allowedTierIDs != nil && !allowedTierIDs[key.TierID]) || !key.HasFeature(feature) {
			continue
		}
		offered = true
		if key.Available() {
			available = append(available, key)
		}
	}
	for len(available) > 0 {
		i := int((m.next.Add(1) - 1) % uint64(len(available)))
		key := available[i]
		if key.TryRate(feature) {
			if ok, didReset, windowStart := key.TryConsumeUsage(); ok {
				if key.UsageLimit != nil {
					m.persistUsage(key.ID, didReset, windowStart)
				}
				return &Served{ID: key.ID, KeyValue: key.KeyValue, Metadata: key.Metadata, Secrets: key.Secrets}, nil
			}
		}
		available = append(available[:i], available[i+1:]...)
	}
	if !offered {
		return nil, ErrOutOfScope
	}
	return nil, ErrExhausted
}

func (m *Manager) persistUsage(keyID string, didReset bool, windowStart time.Time) {
	if didReset {
		m.usage.WindowReset(keyID, windowStart)
		return
	}
	m.usage.Served(keyID)
}

func (m *Manager) ReloadKeys() error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

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

	m.mu.Lock()
	defer m.mu.Unlock()

	existing := make(map[string]*PoolKey, len(m.keys))
	for _, key := range m.keys {
		existing[key.ID] = key
	}
	newKeys := make([]*PoolKey, 0, len(dbKeys))
	for _, k := range dbKeys {
		features := featureLimits(byTier[k.TierID])
		if len(features) == 0 {
			m.logger.Warn().Str("key_id", k.ID).Str("tier_id", k.TierID).Msg("key skipped from pool: tier has no features")
			continue
		}
		key, ok := existing[k.ID]
		if !ok {
			key = &PoolKey{UsageCount: k.UsageCount, UsageWindowStart: k.UsageWindowStart}
		}
		key.load(k, features, m.openSecrets(k.ID, byKey[k.ID]))
		newKeys = append(newKeys, key)
	}
	m.keys = newKeys
	return nil
}

func featureLimits(features []*db.TierFeature) map[string]FeatureLimit {
	limits := make(map[string]FeatureLimit, len(features))
	for _, f := range features {
		limits[f.Feature] = FeatureLimit{RateLimit: f.RateLimit, WindowSeconds: f.WindowSeconds}
	}
	return limits
}

func (m *Manager) openSecrets(keyID string, rows []*db.KeySecret) map[string]string {
	secrets := make(map[string]string, len(rows))
	for _, s := range rows {
		plain, err := m.sealer.Open(s.Value)
		if err != nil {
			m.logger.Error().Err(err).Str("key_id", keyID).Str("secret", s.Name).Msg("failed to open secret")
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

func (m *Manager) TierOf(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if key := m.find(id); key != nil {
		return key.TierID, true
	}
	return "", false
}

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
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	if err := m.dbAdap.SetKeyExhausted(ctx, id, until); err != nil {
		m.logger.Error().Err(err).Str("key_id", id).Msg("failed to persist exhausted_until")
	}
	return true
}

func (m *Manager) PoolSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.keys)
}

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
