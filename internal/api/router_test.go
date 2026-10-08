package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/config"
	"github.com/rs/zerolog"
)

const adminToken = "admin-token"

func TestEveryAdminRouteWantsTheAdminToken(t *testing.T) {
	h := NewRouter(&Server{Cfg: &config.Config{AdminToken: adminToken}, Auth: NewAuthCache(time.Minute), Logger: zerolog.Nop()})
	routes := []string{
		"GET /admin/tiers", "POST /admin/tiers", "PATCH /admin/tiers",
		"GET /admin/keys", "POST /admin/keys", "DELETE /admin/keys/k1", "GET /admin/health",
		"GET /admin/consumers", "POST /admin/consumers", "DELETE /admin/consumers/c1",
		"POST /admin/consumers/c1/scopes", "GET /admin/usage",
	}
	for _, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		for _, token := range []string{"", "not-admin"} {
			r := httptest.NewRequest(method, path, nil)
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s with token %q answered %d, want 401", route, token, w.Code)
			}
		}
	}
}

func TestRequestsAreLoggedOnlyWhenAskedFor(t *testing.T) {
	for _, logRequests := range []bool{false, true} {
		var logged bytes.Buffer
		h := NewRouter(&Server{
			Cfg:    &config.Config{AdminToken: adminToken, LogRequests: logRequests},
			Auth:   NewAuthCache(time.Minute),
			Logger: zerolog.New(&logged),
		})
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
		if got := strings.Contains(logged.String(), "/health"); got != logRequests {
			t.Errorf("LogRequests=%v: logged %q", logRequests, logged.String())
		}
	}
}
