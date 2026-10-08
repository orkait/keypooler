package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/config"
	"github.com/orkait/keypooler/internal/db"
	"github.com/rs/zerolog"
)

// failingCreates refuses every create with err; any other call panics on the nil
// embedded interface.
type failingCreates struct {
	db.DBAdapter
	err error
}

func (f failingCreates) CreateTier(context.Context, *db.Tier, []*db.TierFeature) error {
	return f.err
}
func (f failingCreates) CreateConsumer(context.Context, *db.Consumer) error { return f.err }

func TestACreateIsAConflictOnlyWhenTheNameIsTaken(t *testing.T) {
	routes := map[string]string{
		"/admin/tiers":     `{"name":"t","features":{"chat":{"rate_limit":1}}}`,
		"/admin/consumers": `{"name":"c"}`,
	}
	failures := map[string]struct {
		err    error
		status int
	}{
		"duplicate":     {db.ErrDuplicate, http.StatusConflict},
		"anything-else": {errors.New("connection reset"), http.StatusInternalServerError},
	}
	for path, body := range routes {
		for name, f := range failures {
			t.Run(path+"/"+name, func(t *testing.T) {
				h := NewRouter(&Server{
					DB:     failingCreates{err: f.err},
					Cfg:    &config.Config{AdminToken: adminToken},
					Auth:   NewAuthCache(time.Minute),
					Logger: zerolog.Nop(),
				})
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+adminToken)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != f.status {
					t.Fatalf("status %d, want %d: %s", w.Code, f.status, w.Body)
				}
			})
		}
	}
}
