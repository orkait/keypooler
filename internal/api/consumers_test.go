package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/rs/zerolog"
)

type failingCreates struct {
	db.DBAdapter
	err error
}

func (f failingCreates) CreateTier(context.Context, *db.Tier, []*db.TierFeature) error {
	return f.err
}
func (f failingCreates) CreateConsumer(context.Context, *db.Consumer) error { return f.err }

type scopedConsumers struct {
	db.DBAdapter
	granted map[string]bool
}

func (s *scopedConsumers) GetTierByName(_ context.Context, name string) (*db.Tier, error) {
	if name == "missing" {
		return nil, nil
	}
	return &db.Tier{ID: "id-" + name, Name: name}, nil
}

func (s *scopedConsumers) RemoveConsumerScope(_ context.Context, consumerID, tierID string) (bool, error) {
	removed := s.granted[consumerID+"/"+tierID]
	delete(s.granted, consumerID+"/"+tierID)
	return removed, nil
}

func TestAScopeIsTakenBackOnceThenTheConsumerHasNothingLeftToLose(t *testing.T) {
	store := &scopedConsumers{granted: map[string]bool{"c1/id-groq": true}}
	h := NewRouter(&Server{DB: store, AdminToken: adminToken, Auth: NewAuthCache(time.Minute), Logger: zerolog.Nop()})
	for _, step := range []struct {
		path string
		want int
	}{
		{"/admin/consumers/c1/scopes/groq", http.StatusOK},
		{"/admin/consumers/c1/scopes/groq", http.StatusNotFound},
		{"/admin/consumers/c1/scopes/missing", http.StatusNotFound},
	} {
		r := httptest.NewRequest(http.MethodDelete, step.path, nil)
		r.Header.Set(headerAuthorization, bearer(adminToken))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != step.want {
			t.Fatalf("%s answered %d, want %d: %s", step.path, w.Code, step.want, w.Body)
		}
	}
}

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
					DB:         failingCreates{err: f.err},
					AdminToken: adminToken,
					Auth:       NewAuthCache(time.Minute),
					Logger:     zerolog.Nop(),
				})
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				r.Header.Set(headerAuthorization, bearer(adminToken))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != f.status {
					t.Fatalf("status %d, want %d: %s", w.Code, f.status, w.Body)
				}
			})
		}
	}
}
