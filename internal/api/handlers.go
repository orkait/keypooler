package api

import (
	"net/http"
	"strings"

	"github.com/orkait/keypooler/internal/config"
	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

// Server holds all dependencies needed by HTTP handlers. The handlers live by
// resource: keys.go, tiers.go, consumers.go.
type Server struct {
	DB     db.DBAdapter
	Pool   *keypool.Manager
	Usage  *writeback.Writer
	Auth   *AuthCache
	Cfg    *config.Config
	Sealer *crypto.Sealer
	Logger zerolog.Logger
}

// HealthCheck handles GET /health, the unauthenticated liveness probe.
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Health handles GET /admin/health
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pool_size":  s.Pool.PoolSize(),
		"encryption": s.Sealer.Enabled(),
	})
}

// extractPathParam returns the path segment right after prefix.
func extractPathParam(path, prefix string) string {
	segment, _, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
	return segment
}
