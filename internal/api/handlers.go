package api

import (
	"context"
	"net/http"
	"time"

	"key-pool-system/internal/config"
	"key-pool-system/internal/crypto"
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/util"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Server holds all dependencies needed by HTTP handlers.
type Server struct {
	DB     db.DBAdapter
	Pool   *keypool.Manager
	Cfg    *config.Config
	Sealer *crypto.Sealer
	Logger zerolog.Logger
}

// --- Public endpoints ---

func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetKey handles GET /key?feature=X
// Returns the API key value (decrypted if stored encrypted) for the given
// feature, or 429 if none available.
//
// Auth is admin-OR-consumer (resolved by resolveKeyCaller, NOT AdminAuth):
//   - admin token  -> superuser, may fetch any tier's keys (nil scope filter),
//     consumer id recorded as "admin".
//   - consumer token -> may fetch only keys whose tier is in its consumer_scopes.
//     401 for an unknown/inactive token; 403 when no scoped tier serves the
//     feature with budget.
func (s *Server) GetKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	caller, ok := s.resolveKeyCaller(w, r)
	if !ok {
		return // resolveKeyCaller already wrote the 401
	}

	feature := r.URL.Query().Get("feature")
	if feature == "" {
		writeError(w, http.StatusBadRequest, "feature query param required")
		return
	}

	key := s.Pool.GetKeyForFeature(feature, caller.allowedTierIDs)
	if key == nil {
		// A scoped consumer that got nothing may simply not be scoped to any tier
		// that serves this feature -> that is an authorization (403) signal, not a
		// transient 429. The admin (nil scope) only ever hits a true 429.
		if caller.allowedTierIDs != nil {
			writeError(w, http.StatusForbidden, "no key available for feature within your scope")
			return
		}
		writeError(w, http.StatusTooManyRequests, "no key available for feature")
		return
	}

	value, err := s.Sealer.Open(key.KeyValue)
	if err != nil {
		s.Logger.Error().Err(err).Str("key_id", key.ID).Msg("failed to open key value")
		writeError(w, http.StatusInternalServerError, "failed to read key")
		return
	}

	// Audit the serve asynchronously: best-effort, never blocks the response.
	s.recordUsageEventAsync(key.ID, caller.consumerID, feature)

	// Secrets are opened on load by the manager; return them at this trusted
	// boundary alongside the key value and metadata.
	secrets := key.Secrets
	if secrets == nil {
		secrets = map[string]string{}
	}
	metadata := key.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key_id":   key.ID,
		"value":    value,
		"metadata": metadata,
		"secrets":  secrets,
	})
}

// recordUsageEventAsync writes a usage_event in a detached goroutine so the audit
// write never adds latency to the /key response. Failures are logged only.
func (s *Server) recordUsageEventAsync(keyID, consumerID, feature string) {
	go func() {
		ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
		defer cancel()
		if err := s.DB.RecordUsageEvent(ctx, keyID, consumerID, feature); err != nil {
			s.Logger.Error().Err(err).Str("key_id", keyID).Msg("failed to record usage event")
		}
	}()
}

// --- Admin endpoints ---

// CreateTier handles POST /admin/tiers
func (s *Server) CreateTier(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Name        string                      `json:"name"`
		Description string                      `json:"description"`
		Features    map[string]featureLimitBody `json:"features"`
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

	tier := &db.Tier{ID: uuid.New().String(), Name: body.Name, Description: body.Description}
	if err := s.DB.CreateTier(ctx, tier); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create tier")
		return
	}

	features := make([]*db.TierFeature, 0, len(body.Features))
	respFeatures := make(map[string]any, len(body.Features))
	for feature, fl := range body.Features {
		window := fl.WindowSeconds
		if window <= 0 {
			window = 60
		}
		features = append(features, &db.TierFeature{
			TierID:        tier.ID,
			Feature:       feature,
			RateLimit:     fl.RateLimit,
			WindowSeconds: window,
		})
		respFeatures[feature] = map[string]any{"rate_limit": fl.RateLimit, "window_seconds": window}
	}
	if err := s.DB.SetTierFeatures(ctx, tier.ID, features); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set features")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          tier.ID,
		"name":        tier.Name,
		"description": tier.Description,
		"features":    respFeatures,
	})
}

// UpdateTierFeatures replaces the feature set (rate_limit + window_seconds) of an
// existing tier, then reloads the pool so the new limits take effect immediately
// for keys already in that tier.
func (s *Server) UpdateTierFeatures(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Name        string                      `json:"name"`
		Description *string                     `json:"description"`
		Features    map[string]featureLimitBody `json:"features"`
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

	tier, err := s.DB.GetTierByName(ctx, body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if tier == nil {
		writeError(w, http.StatusNotFound, "tier not found: "+body.Name)
		return
	}

	// Update description only when the field is present in the request.
	if body.Description != nil {
		if err := s.DB.UpdateTierDescription(ctx, tier.ID, *body.Description); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update description")
			return
		}
		tier.Description = *body.Description
	}

	features := make([]*db.TierFeature, 0, len(body.Features))
	respFeatures := make(map[string]any, len(body.Features))
	for feature, fl := range body.Features {
		window := fl.WindowSeconds
		if window <= 0 {
			window = 60
		}
		features = append(features, &db.TierFeature{
			TierID:        tier.ID,
			Feature:       feature,
			RateLimit:     fl.RateLimit,
			WindowSeconds: window,
		})
		respFeatures[feature] = map[string]any{"rate_limit": fl.RateLimit, "window_seconds": window}
	}
	if err := s.DB.SetTierFeatures(ctx, tier.ID, features); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set features")
		return
	}

	// Apply the new limits to the in-memory pool right away.
	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Str("tier", body.Name).Msg("failed to reload pool after tier update")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          tier.ID,
		"name":        tier.Name,
		"description": tier.Description,
		"features":    respFeatures,
	})
}

// featureLimitBody is the per-feature rate-limit shape in tier requests.
type featureLimitBody struct {
	RateLimit     int `json:"rate_limit"`
	WindowSeconds int `json:"window_seconds"`
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
		fm := make(map[string]any)
		for _, f := range features {
			fm[f.Feature] = map[string]any{"rate_limit": f.RateLimit, "window_seconds": f.WindowSeconds}
		}
		result[i] = map[string]any{"id": t.ID, "name": t.Name, "description": t.Description, "features": fm}
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
		Name               string            `json:"name"`
		Key                string            `json:"key"`
		Tier               string            `json:"tier"`
		ExpiresAt          *string           `json:"expires_at"`
		UsageLimit         *int              `json:"usage_limit"`
		UsageWindowSeconds *int              `json:"usage_window_seconds"`
		Metadata           map[string]any    `json:"metadata"`
		Secrets            map[string]string `json:"secrets"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name == "" || body.Key == "" || body.Tier == "" {
		writeError(w, http.StatusBadRequest, "name, key, and tier are required")
		return
	}

	var expiresAt *time.Time
	if body.ExpiresAt != nil && *body.ExpiresAt != "" {
		t, perr := time.Parse(time.RFC3339, *body.ExpiresAt)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		expiresAt = &t
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

	sealedKey, err := s.Sealer.Seal(body.Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal key")
		return
	}

	// Seal each bound secret (encrypted iff encryption is enabled) before persisting.
	sealedSecrets := make([]*db.KeySecret, 0, len(body.Secrets))
	keyID := uuid.New().String()
	for name, value := range body.Secrets {
		sealedVal, eerr := s.Sealer.Seal(value)
		if eerr != nil {
			writeError(w, http.StatusInternalServerError, "failed to seal secret")
			return
		}
		sealedSecrets = append(sealedSecrets, &db.KeySecret{KeyID: keyID, Name: name, Value: sealedVal})
	}

	metadata := body.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	key := &db.Key{
		ID:                 keyID,
		Name:               body.Name,
		KeyValue:           sealedKey,
		TierID:             tier.ID,
		IsActive:           true,
		ExpiresAt:          expiresAt,
		UsageLimit:         body.UsageLimit,
		UsageCount:         0,
		UsageWindowSeconds: body.UsageWindowSeconds,
		UsageWindowStart:   nil,
		Metadata:           metadata,
	}
	if err := s.DB.CreateKey(ctx, key); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}

	if len(sealedSecrets) > 0 {
		if err := s.DB.SetKeySecrets(ctx, key.ID, sealedSecrets); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store key secrets")
			return
		}
	}

	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Msg("failed to reload key pool after adding key")
	}

	secretNames := make([]string, 0, len(sealedSecrets))
	for _, sec := range sealedSecrets {
		secretNames = append(secretNames, sec.Name)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                   key.ID,
		"name":                 key.Name,
		"tier":                 body.Tier,
		"expires_at":           body.ExpiresAt,
		"usage_limit":          body.UsageLimit,
		"usage_window_seconds": body.UsageWindowSeconds,
		"metadata":             metadata,
		"secret_names":         secretNames,
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
			usage[feature] = map[string]any{
				"used":           info.Used,
				"limit":          info.Limit,
				"window_seconds": info.WindowSeconds,
			}
		}
		metadata := ks.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		secretNames := ks.SecretNames
		if secretNames == nil {
			secretNames = []string{}
		}
		var expiresAt any
		if ks.ExpiresAt != nil {
			expiresAt = ks.ExpiresAt.UTC().Format(time.RFC3339)
		}
		result[i] = map[string]any{
			"id":           ks.ID,
			"name":         ks.Name,
			"tier_id":      ks.TierID,
			"is_active":    ks.IsActive,
			"expires_at":   expiresAt,
			"usage_limit":  ks.UsageLimit,
			"usage_count":  ks.UsageCount,
			"metadata":     metadata,
			"secret_names": secretNames,
			"usage":        usage,
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

// (uncapped decodeJSONBody removed; all handlers use the 1 MB-capped decodeJSON)
