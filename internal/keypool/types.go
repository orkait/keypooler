package keypool

import (
	"sync"
	"time"
)

// PoolKey is a runtime representation of an API key with rate tracking.
type PoolKey struct {
	ID           string
	Name         string
	KeyEncrypted string
	TierID       string
	IsActive     bool

	// Features and their rate limits (from tier)
	Features map[string]int // feature -> rate_per_minute

	mu           sync.Mutex
	rateCounters map[string]*rateCounter // feature -> counter
}

type rateCounter struct {
	count       int
	windowStart time.Time
}

// TryRate checks if the key has available rate for the given feature.
// Returns true and increments counter if allowed.
func (k *PoolKey) TryRate(feature string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	limit, ok := k.Features[feature]
	if !ok {
		return false // key doesn't support this feature
	}

	if k.rateCounters == nil {
		k.rateCounters = make(map[string]*rateCounter)
	}

	rc, ok := k.rateCounters[feature]
	if !ok {
		rc = &rateCounter{}
		k.rateCounters[feature] = rc
	}

	now := time.Now()
	if now.Sub(rc.windowStart) >= time.Minute {
		rc.count = 0
		rc.windowStart = now
	}

	if rc.count >= limit {
		return false
	}

	rc.count++
	return true
}

// HasFeature returns true if the key's tier supports the given feature.
func (k *PoolKey) HasFeature(feature string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.Features[feature]
	return ok
}

// RateUsage returns current usage for each feature.
func (k *PoolKey) RateUsage() map[string]RateInfo {
	k.mu.Lock()
	defer k.mu.Unlock()

	usage := make(map[string]RateInfo, len(k.Features))
	for feature, limit := range k.Features {
		info := RateInfo{Limit: limit}
		if rc, ok := k.rateCounters[feature]; ok {
			now := time.Now()
			if now.Sub(rc.windowStart) < time.Minute {
				info.Used = rc.count
				info.WindowStart = rc.windowStart
			}
		}
		usage[feature] = info
	}
	return usage
}

// RateInfo holds rate usage info for a single feature.
type RateInfo struct {
	Used        int
	Limit       int
	WindowStart time.Time
}
