package keypool

import (
	"sync"
	"time"

	"github.com/orkait/keypooler/internal/db"
)

type FeatureLimit struct {
	RateLimit     int
	WindowSeconds int
}

type PoolKey struct {
	ID       string
	Name     string
	KeyValue string
	TierID   string
	IsActive bool

	ExpiresAt          *time.Time
	UsageLimit         *int
	UsageCount         int
	UsageWindowSeconds *int
	UsageWindowStart   *time.Time
	ExhaustedUntil     *time.Time
	Metadata           map[string]any
	Secrets            map[string]string
	Features           map[string]FeatureLimit

	mu           sync.Mutex
	rateCounters map[string]*rateCounter
}

// load keeps the usage count: it runs ahead of the database by unflushed writeback.
func (k *PoolKey) load(row *db.Key, features map[string]FeatureLimit, secrets map[string]string) {
	k.ID = row.ID
	k.Name = row.Name
	k.KeyValue = row.KeyValue
	k.TierID = row.TierID
	k.IsActive = row.IsActive
	k.ExpiresAt = row.ExpiresAt
	k.UsageLimit = row.UsageLimit
	k.UsageWindowSeconds = row.UsageWindowSeconds
	k.ExhaustedUntil = row.ExhaustedUntil
	k.Metadata = row.Metadata
	k.Secrets = secrets
	k.Features = features
}

type rateCounter struct {
	count       int
	windowStart time.Time
}

func (fl FeatureLimit) window() time.Duration {
	if fl.WindowSeconds <= 0 {
		return time.Minute
	}
	return time.Duration(fl.WindowSeconds) * time.Second
}

func (k *PoolKey) TryRate(feature string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	fl, ok := k.Features[feature]
	if !ok {
		return false
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
	if now.Sub(rc.windowStart) >= fl.window() {
		rc.count = 0
		rc.windowStart = now
	}
	if rc.count >= fl.RateLimit {
		return false
	}
	rc.count++
	return true
}

func (k *PoolKey) Available() bool {
	now := time.Now()
	return k.IsActive &&
		(k.ExpiresAt == nil || now.Before(*k.ExpiresAt)) &&
		(k.ExhaustedUntil == nil || !now.Before(*k.ExhaustedUntil))
}

func (k *PoolKey) TryConsumeUsage() (ok bool, didReset bool, windowStart time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.UsageLimit == nil {
		return true, false, time.Time{}
	}
	if k.UsageWindowSeconds != nil && *k.UsageWindowSeconds > 0 {
		now := time.Now()
		window := time.Duration(*k.UsageWindowSeconds) * time.Second
		if k.UsageWindowStart == nil || now.Sub(*k.UsageWindowStart) >= window {
			k.UsageCount = 0
			k.UsageWindowStart = &now
			didReset, windowStart = true, now
		}
	}
	if k.UsageCount >= *k.UsageLimit {
		return false, didReset, windowStart
	}
	k.UsageCount++
	return true, didReset, windowStart
}

func (k *PoolKey) UsageSnapshot() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.UsageCount
}

func (k *PoolKey) HasFeature(feature string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.Features[feature]
	return ok
}

func (k *PoolKey) RateUsage() map[string]RateInfo {
	k.mu.Lock()
	defer k.mu.Unlock()

	usage := make(map[string]RateInfo, len(k.Features))
	for feature, fl := range k.Features {
		info := RateInfo{Limit: fl.RateLimit, WindowSeconds: fl.WindowSeconds}
		if rc, ok := k.rateCounters[feature]; ok && time.Since(rc.windowStart) < fl.window() {
			info.Used = rc.count
		}
		usage[feature] = info
	}
	return usage
}

type RateInfo struct {
	Used          int
	Limit         int
	WindowSeconds int
}
