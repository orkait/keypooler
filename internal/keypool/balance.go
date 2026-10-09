package keypool

import "time"

type Balance struct {
	Used, Limit float64
	Unit        string
	ResetsAt    *time.Time
	CheckedAt   time.Time
}

func (b Balance) Spent() bool {
	return b.Limit > 0 && b.Used >= b.Limit
}

type Held struct {
	ID       string
	KeyValue string
}

func (m *Manager) Holding(feature string) []Held {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var held []Held
	for _, key := range m.keys {
		if !key.IsActive || !key.HasFeature(feature) {
			continue
		}
		value, err := m.sealer.Open(key.KeyValue)
		if err != nil {
			m.logger.Error().Err(err).Str("key_id", key.ID).Msg("failed to open key")
			continue
		}
		held = append(held, Held{ID: key.ID, KeyValue: value})
	}
	return held
}

func (m *Manager) SetBalance(id string, b Balance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key := m.find(id); key != nil {
		key.Balance = &b
	}
}
