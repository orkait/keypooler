package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

type Server struct {
	DB         db.DBAdapter
	Pool       *keypool.Manager
	Usage      *writeback.Writer
	Auth       *AuthCache
	AdminToken string
	Sealer     *crypto.Sealer
	Logger     zerolog.Logger
}

func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: statusOK})
}

func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, poolHealth{PoolSize: s.Pool.PoolSize(), Encryption: s.Sealer.Enabled()})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set(headerContentType, mediaJSON)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message})
}

func decodeJSON(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodySize)
	return json.NewDecoder(r.Body).Decode(dst)
}

func parseLimit(raw string, def, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}
