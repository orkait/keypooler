package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/rs/zerolog"
)

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
		DB:         store,
		AdminToken: adminToken,
		Auth:       NewAuthCache(time.Minute),
		Logger:     zerolog.Nop(),
	}
}

func resolve(s *Server, header string) (keyCaller, int) {
	r := httptest.NewRequest(http.MethodGet, "/key?feature=chat", nil)
	if header != "" {
		r.Header.Set(headerAuthorization, header)
	}
	w := httptest.NewRecorder()
	caller, _ := s.resolveKeyCaller(w, r)
	return caller, w.Code
}

func TestAKeyCallerIsTheAdminAScopedConsumerOrRefused(t *testing.T) {
	cases := map[string]struct {
		header   string
		active   bool
		consumer string
		status   int
	}{
		"admin-token":      {header: bearer(adminToken), consumer: adminConsumerID, status: http.StatusOK},
		"consumer-token":   {header: bearer(consumerToken), active: true, consumer: "c1", status: http.StatusOK},
		"lowercase-scheme": {header: "bearer " + consumerToken, active: true, consumer: "c1", status: http.StatusOK},
		"unknown-token":    {header: bearer(consumerToken), status: http.StatusUnauthorized},
		"no-header":        {status: http.StatusUnauthorized},
		"not-bearer":       {header: "Basic " + consumerToken, status: http.StatusUnauthorized},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			caller, status := resolve(authServer(&consumerDB{active: c.active}), c.header)
			if status != c.status || caller.consumerID != c.consumer {
				t.Fatalf("status %d caller %+v", status, caller)
			}
			admin := c.consumer == adminConsumerID
			if c.consumer != "" && (caller.allowedTierIDs == nil) != admin {
				t.Fatalf("scope %v", caller.allowedTierIDs)
			}
		})
	}
}

func TestAConsumerIsLookedUpOnceUntilItsEntryExpires(t *testing.T) {
	store := &consumerDB{active: true}
	s := authServer(store)
	resolve(s, bearer(consumerToken))
	if cached, _ := resolve(s, bearer(consumerToken)); store.lookups != 1 || !cached.allowedTierIDs["groq_chat"] {
		t.Fatalf("lookups %d, cached %+v", store.lookups, cached)
	}
	s.Auth = NewAuthCache(time.Millisecond)
	resolve(s, bearer(consumerToken))
	time.Sleep(5 * time.Millisecond)
	resolve(s, bearer(consumerToken))
	if store.lookups != 3 {
		t.Fatalf("lookups %d, want 3", store.lookups)
	}
}

func TestAnAdminWriteForgetsEveryConsumerSoARevocationTakesHold(t *testing.T) {
	store := &consumerDB{active: true}
	s := authServer(store)
	resolve(s, bearer(consumerToken))

	store.active = false
	admin := httptest.NewRequest(http.MethodPost, "/admin/tiers", strings.NewReader("not json"))
	admin.Header.Set(headerAuthorization, bearer(adminToken))
	NewRouter(s).ServeHTTP(httptest.NewRecorder(), admin)

	if _, status := resolve(s, bearer(consumerToken)); status != http.StatusUnauthorized {
		t.Fatalf("revoked consumer answered %d, want 401", status)
	}
}
