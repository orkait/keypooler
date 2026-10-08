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
