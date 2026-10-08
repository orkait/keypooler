package api

import (
	"net/http"
	"sync"
	"time"
)

// AuthCache keeps resolved consumer tokens in memory, so a /key call does no
// database lookup for a token seen within ttl. Any admin write clears it whole:
// consumers, scopes and tiers only change through the admin API of this single
// replica, so a revocation takes hold on the next call.
type AuthCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]authEntry
}

type authEntry struct {
	caller  keyCaller
	expires time.Time
}

func NewAuthCache(ttl time.Duration) *AuthCache {
	return &AuthCache{ttl: ttl, entries: map[string]authEntry{}}
}

func (c *AuthCache) get(tokenHash string) (keyCaller, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[tokenHash]
	if !ok || time.Now().After(e.expires) {
		return keyCaller{}, false
	}
	return e.caller, true
}

func (c *AuthCache) put(tokenHash string, caller keyCaller) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[tokenHash] = authEntry{caller: caller, expires: time.Now().Add(c.ttl)}
}

// Clear forgets every cached consumer.
func (c *AuthCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]authEntry{}
}

// clearAfterWrite clears the cache once an admin request that can change state has
// been handled, whatever it answered.
func (c *AuthCache) clearAfterWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			c.Clear()
		}
	})
}
