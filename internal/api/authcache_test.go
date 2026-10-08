package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/config"
	"github.com/orkait/keypooler/internal/db"
	"github.com/rs/zerolog"
)

// consumerDB answers only the two auth lookups; any other call panics on the nil
// embedded interface, which is what a test reaching it deserves.
type consumerDB struct {
	db.DBAdapter
	mu      sync.Mutex
	lookups int
	active  bool
}

func (c *consumerDB) GetConsumerByTokenHash(_ context.Context, hash string) (*db.Consumer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lookups++
	if !c.active {
		return nil, nil
	}
	return &db.Consumer{ID: "c1", Name: "ai-gateway", TokenHash: hash, IsActive: true}, nil
}

func (c *consumerDB) GetConsumerScopes(context.Context, string) ([]string, error) {
	return []string{"groq_chat"}, nil
}

const consumerToken = "consumer-token"

func authServer(store *consumerDB) *Server {
	return &Server{
		DB:     store,
		Cfg:    &config.Config{AdminToken: "admin-token"},
		Auth:   NewAuthCache(time.Minute),
		Logger: zerolog.Nop(),
	}
}

func resolve(t *testing.T, s *Server) (keyCaller, int) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/key?feature=chat", nil)
	r.Header.Set("Authorization", "Bearer "+consumerToken)
	w := httptest.NewRecorder()
	caller, _ := s.resolveKeyCaller(w, r)
	return caller, w.Code
}

func TestAKeyCallerIsTheAdminAScopedConsumerOrRefused(t *testing.T) {
	cases := map[string]struct {
		header   string
		active   bool
		admin    bool
		consumer string
		status   int
	}{
		"admin-token":      {header: "Bearer admin-token", admin: true, consumer: adminConsumerID, status: http.StatusOK},
		"consumer-token":   {header: "Bearer " + consumerToken, active: true, consumer: "c1", status: http.StatusOK},
		"lowercase-scheme": {header: "bearer " + consumerToken, active: true, consumer: "c1", status: http.StatusOK},
		"unknown-token":    {header: "Bearer " + consumerToken, status: http.StatusUnauthorized},
		"no-header":        {header: "", status: http.StatusUnauthorized},
		"not-bearer":       {header: "Basic " + consumerToken, status: http.StatusUnauthorized},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := authServer(&consumerDB{active: c.active})
			r := httptest.NewRequest(http.MethodGet, "/key?feature=chat", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			w := httptest.NewRecorder()
			caller, ok := s.resolveKeyCaller(w, r)
			if ok != (c.status == http.StatusOK) || w.Code != c.status {
				t.Fatalf("ok %v, status %d, want %d", ok, w.Code, c.status)
			}
			if ok && (caller.isAdmin != c.admin || caller.consumerID != c.consumer || (!c.admin && !caller.allowedTierIDs["groq_chat"])) {
				t.Fatalf("caller %+v", caller)
			}
		})
	}
}

func TestAConsumerIsLookedUpOnceThenServedFromMemory(t *testing.T) {
	store := &consumerDB{active: true}
	s := authServer(store)
	first, _ := resolve(t, s)
	second, _ := resolve(t, s)
	if store.lookups != 1 {
		t.Fatalf("lookups %d, want 1", store.lookups)
	}
	if second.consumerID != "c1" || !second.allowedTierIDs["groq_chat"] || first.consumerID != second.consumerID {
		t.Fatalf("cached caller %+v", second)
	}
}

func TestAnAdminWriteForgetsEveryConsumerSoARevocationTakesHold(t *testing.T) {
	store := &consumerDB{active: true}
	s := authServer(store)
	resolve(t, s)

	store.active = false
	admin := httptest.NewRequest(http.MethodPost, "/admin/tiers", strings.NewReader("not json"))
	admin.Header.Set("Authorization", "Bearer admin-token")
	NewRouter(s).ServeHTTP(httptest.NewRecorder(), admin)

	if _, code := resolve(t, s); code != http.StatusUnauthorized {
		t.Fatalf("revoked consumer answered %d, want 401", code)
	}
}

func TestAnAdminReadKeepsTheCache(t *testing.T) {
	store := &consumerDB{active: true}
	s := authServer(store)
	resolve(t, s)
	s.Auth.clearAfterWrite(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/consumers", nil))
	resolve(t, s)
	if store.lookups != 1 {
		t.Fatalf("lookups %d, want 1", store.lookups)
	}
}

func TestACachedConsumerExpires(t *testing.T) {
	store := &consumerDB{active: true}
	s := authServer(store)
	s.Auth = NewAuthCache(time.Millisecond)
	resolve(t, s)
	time.Sleep(5 * time.Millisecond)
	resolve(t, s)
	if store.lookups != 2 {
		t.Fatalf("lookups %d, want 2", store.lookups)
	}
}
