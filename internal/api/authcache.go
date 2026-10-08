package api

import (
	"net/http"
	"sync"
	"time"
)

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

func (c *AuthCache) clearAfterWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			c.mu.Lock()
			c.entries = map[string]authEntry{}
			c.mu.Unlock()
		}
	})
}
