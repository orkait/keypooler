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

func routerUnderTest() http.Handler {
	return NewRouter(&Server{
		Cfg:    &config.Config{AdminToken: adminToken},
		Auth:   NewAuthCache(time.Minute),
		Logger: zerolog.Nop(),
	})
}

func status(h http.Handler, method, path, token string) int {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestEveryAdminRouteWantsTheAdminToken(t *testing.T) {
	h := routerUnderTest()
	routes := []struct{ method, path string }{
		{http.MethodGet, "/admin/tiers"},
		{http.MethodPost, "/admin/tiers"},
		{http.MethodPatch, "/admin/tiers"},
		{http.MethodGet, "/admin/keys"},
		{http.MethodPost, "/admin/keys"},
		{http.MethodDelete, "/admin/keys/k1"},
		{http.MethodGet, "/admin/health"},
		{http.MethodGet, "/admin/consumers"},
		{http.MethodPost, "/admin/consumers"},
		{http.MethodDelete, "/admin/consumers/c1"},
		{http.MethodPost, "/admin/consumers/c1/scopes"},
		{http.MethodGet, "/admin/usage"},
	}
	for _, route := range routes {
		for _, token := range []string{"", "not-admin"} {
			if got := status(h, route.method, route.path, token); got != http.StatusUnauthorized {
				t.Errorf("%s %s with token %q answered %d, want 401", route.method, route.path, token, got)
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
		status(h, http.MethodGet, "/health", "")
		if got := strings.Contains(logged.String(), "/health"); got != logRequests {
			t.Errorf("LogRequests=%v: logged %q", logRequests, logged.String())
		}
	}
}

func TestARouteAnswersOnlyItsOwnMethods(t *testing.T) {
	h := routerUnderTest()
	wrong := []struct{ method, path string }{
		{http.MethodPut, "/admin/tiers"},
		{http.MethodDelete, "/admin/keys"},
		{http.MethodGet, "/admin/keys/k1"},
		{http.MethodPost, "/admin/health"},
		{http.MethodGet, "/admin/consumers/c1/scopes"},
		{http.MethodDelete, "/admin/usage"},
		{http.MethodPost, "/key"},
		{http.MethodGet, "/key/k1/exhausted"},
	}
	for _, route := range wrong {
		if got := status(h, route.method, route.path, adminToken); got != http.StatusMethodNotAllowed {
			t.Errorf("%s %s answered %d, want 405", route.method, route.path, got)
		}
	}
}
