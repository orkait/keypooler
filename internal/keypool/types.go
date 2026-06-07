package keypool

import (
	"sync"
	"time"
)

// FeatureLimit is a per-feature rate limit with a configurable window.
type FeatureLimit struct {
	RateLimit     int
	WindowSeconds int
}

// PoolKey is a runtime representation of an API key with rate tracking.
type PoolKey struct {
	ID           string
	Name         string
	KeyEncrypted string
	TierID       string
	IsActive     bool

	ExpiresAt  *time.Time
	UsageLimit *int
	UsageCount int
	// UsageWindowSeconds, when set, turns UsageLimit into a per-window budget
	// (e.g. 2592000 = 30 days). nil means UsageLimit is a lifetime cap.
	UsageWindowSeconds *int
	// UsageWindowStart marks the start of the current usage window. nil until the
	// first window opens.
	UsageWindowStart *time.Time
	Metadata         map[string]any

	// Secrets are decrypted name->value pairs, populated by the manager on load.
	Secrets map[string]string

	// Features and their window-aware rate limits (from tier).
	Features map[string]FeatureLimit

	mu           sync.Mutex
	rateCounters map[string]*rateCounter // feature -> counter
}

type rateCounter struct {
	count       int
	windowStart time.Time
}

// window returns the rate-limit window for a feature, defaulting to one minute.
func (fl FeatureLimit) window() time.Duration {
	if fl.WindowSeconds <= 0 {
		return time.Minute
	}
	return time.Duration(fl.WindowSeconds) * time.Second
}

// TryRate checks if the key has available rate for the given feature.
// Returns true and increments counter if allowed. The window is the
// feature's configured WindowSeconds.
func (k *PoolKey) TryRate(feature string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	fl, ok := k.Features[feature]
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

// Available reports the static gate: the key must be active and not expired.
// The cumulative usage gate is intentionally NOT here - it is checked and
// consumed atomically in TryConsumeUsage so the check-and-increment cannot race.
// (IsActive/ExpiresAt are mutated only under the manager's write lock and read
// here under its read lock, so they need no per-key lock.)
func (k *PoolKey) Available() bool {
	if !k.IsActive {
		return false
	}
	if k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt) {
		return false
	}
	return true
}

// TryConsumeUsage atomically checks and consumes one unit of the key's
// cumulative usage budget. A key with no usage limit always succeeds and is not
// counted.
//
// Window behaviour: when UsageWindowSeconds is set and the current window has
// elapsed (now - UsageWindowStart >= window), the in-memory count is reset to 0
// and the window restarts at now BEFORE the limit check. The reset is reported
// via the returned didReset/windowStart so the caller can persist it. When
// UsageWindowSeconds is nil the limit is a lifetime cap (the original behaviour).
//
// The window-check, reset, limit-check, and increment all happen in one critical
// section under the key lock so concurrent callers cannot over-serve a limited
// key and cannot double-reset a window.
func (k *PoolKey) TryConsumeUsage() (ok bool, didReset bool, windowStart time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.UsageLimit == nil {
		return true, false, time.Time{}
	}

	if k.UsageWindowSeconds != nil && *k.UsageWindowSeconds > 0 {
		now := time.Now()
		window := time.Duration(*k.UsageWindowSeconds) * time.Second
		// A nil window start means the window has never opened; open it now.
		// Otherwise roll the window over once it has fully elapsed.
		if k.UsageWindowStart == nil || now.Sub(*k.UsageWindowStart) >= window {
			k.UsageCount = 0
			start := now
			k.UsageWindowStart = &start
			didReset = true
			windowStart = start
		}
	}

	if k.UsageCount >= *k.UsageLimit {
		return false, didReset, windowStart
	}
	k.UsageCount++
	return true, didReset, windowStart
}

// UsageSnapshot reads the cumulative usage count under the key lock (for the
// admin/health view, which must not read UsageCount concurrently with a consume).
func (k *PoolKey) UsageSnapshot() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.UsageCount
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
	for feature, fl := range k.Features {
		info := RateInfo{Limit: fl.RateLimit, WindowSeconds: fl.WindowSeconds}
		if rc, ok := k.rateCounters[feature]; ok {
			now := time.Now()
			if now.Sub(rc.windowStart) < fl.window() {
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
	Used          int
	Limit         int
	WindowSeconds int
	WindowStart   time.Time
}
