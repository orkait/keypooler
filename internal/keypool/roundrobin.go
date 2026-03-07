package keypool

import "sync"

// RoundRobin selects keys in a simple rotating order.
type RoundRobin struct {
	mu      sync.Mutex
	counter uint64
}

func NewRoundRobin() *RoundRobin {
	return &RoundRobin{}
}

func (r *RoundRobin) Select(keys []*PoolKey) *PoolKey {
	if len(keys) == 0 {
		return nil
	}

	r.mu.Lock()
	idx := r.counter % uint64(len(keys))
	r.counter++
	r.mu.Unlock()

	return keys[idx]
}
