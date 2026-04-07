package api

import (
	"encoding/json"
	"net/http"

	"key-pool-system/internal/config"
	"key-pool-system/internal/crypto"
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Server holds all dependencies needed by HTTP handlers.
type Server struct {
	DB     db.DBAdapter
	Pool   *keypool.Manager
	Cfg    *config.Config
	Logger zerolog.Logger
}

// --- Public endpoints ---

func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetKey handles GET /key?feature=X
// Returns a decrypted API key for the given feature, or 429 if none available.
func (s *Server) GetKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	feature := r.URL.Query().Get("feature")
	if feature == "" {
		writeError(w, http.StatusBadRequest, "feature query param required")
		return
	}

	key := s.Pool.GetKeyForFeature(feature)
	if key == nil {
		writeError(w, http.StatusTooManyRequests, "no key available for feature")
		return
	}

	decrypted, err := crypto.Decrypt(key.KeyEncrypted, s.Cfg.EncryptionKey)
	if err != nil {
		s.Logger.Error().Err(err).Str("key_id", key.ID).Msg("failed to decrypt key")
		writeError(w, http.StatusInternalServerError, "failed to decrypt key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"key_id": key.ID,
		"value":  decrypted,
	})
}

// --- Admin endpoints ---

// CreateTier handles POST /admin/tiers
func (s *Server) CreateTier(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Name     string         `json:"name"`
		Features map[string]int `json:"features"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name == "" || len(body.Features) == 0 {
		writeError(w, http.StatusBadRequest, "name and features are required")
		return
	}

	ctx := r.Context()

	existing, err := s.DB.GetTierByName(ctx, body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "tier already exists: "+body.Name)
		return
	}

	tier := &db.Tier{ID: uuid.New().String(), Name: body.Name}
	if err := s.DB.CreateTier(ctx, tier); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create tier")
		return
	}

	features := make([]*db.TierFeature, 0, len(body.Features))
	for feature, rate := range body.Features {
		features = append(features, &db.TierFeature{TierID: tier.ID, Feature: feature, RatePerMinute: rate})
	}
	if err := s.DB.SetTierFeatures(ctx, tier.ID, features); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set features")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       tier.ID,
		"name":     tier.Name,
		"features": body.Features,
	})
}

// ListTiers handles GET /admin/tiers
func (s *Server) ListTiers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	tiers, err := s.DB.GetAllTiers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	result := make([]map[string]any, len(tiers))
	for i, t := range tiers {
		features, _ := s.DB.GetTierFeatures(ctx, t.ID)
		fm := make(map[string]int)
		for _, f := range features {
			fm[f.Feature] = f.RatePerMinute
		}
		result[i] = map[string]any{"id": t.ID, "name": t.Name, "features": fm}
	}
	writeJSON(w, http.StatusOK, result)
}

// AddKey handles POST /admin/keys
func (s *Server) AddKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Name string `json:"name"`
		Key  string `json:"key"`
		Tier string `json:"tier"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name == "" || body.Key == "" || body.Tier == "" {
		writeError(w, http.StatusBadRequest, "name, key, and tier are required")
		return
	}

	ctx := r.Context()

	tier, err := s.DB.GetTierByName(ctx, body.Tier)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if tier == nil {
		writeError(w, http.StatusNotFound, "tier not found: "+body.Tier)
		return
	}

	encrypted, err := crypto.Encrypt(body.Key, s.Cfg.EncryptionKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encrypt key")
		return
	}

	key := &db.Key{
		ID:           uuid.New().String(),
		Name:         body.Name,
		KeyEncrypted: encrypted,
		TierID:       tier.ID,
		IsActive:     true,
	}
	if err := s.DB.CreateKey(ctx, key); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}

	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Msg("failed to reload key pool after adding key")
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":   key.ID,
		"name": key.Name,
		"tier": body.Tier,
	})
}

// ListKeys handles GET /admin/keys
func (s *Server) ListKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	statuses := s.Pool.GetHealthStatus()
	result := make([]map[string]any, len(statuses))
	for i, ks := range statuses {
		usage := make(map[string]any)
		for feature, info := range ks.Usage {
			usage[feature] = map[string]any{"used": info.Used, "limit": info.Limit}
		}
		result[i] = map[string]any{
			"id":        ks.ID,
			"name":      ks.Name,
			"tier_id":   ks.TierID,
			"is_active": ks.IsActive,
			"usage":     usage,
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// DeleteKey handles DELETE /admin/keys/{id}
func (s *Server) DeleteKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := extractPathParam(r.URL.Path, "/admin/keys/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "key id required")
		return
	}

	ctx := r.Context()
	if err := s.DB.DeleteKey(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}

	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Msg("failed to reload key pool after deleting key")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Health handles GET /admin/health
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pool_size": s.Pool.PoolSize(),
	})
}

// --- Helpers ---

func extractPathParam(path, prefix string) string {
	trimmed := path[len(prefix):]
	if idx := len(trimmed); idx > 0 {
		for i, c := range trimmed {
			if c == '/' {
				return trimmed[:i]
			}
		}
	}
	return trimmed
}

func decodeJSONBody(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
