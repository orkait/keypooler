package api

import (
	"net/http"
	"time"

	"github.com/orkait/keypooler/internal/db"

	"github.com/google/uuid"
)

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

	s.Usage.Event(db.NewUsageEvent(key.ID, caller.consumerID, feature))

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

// addKeyBody is what POST /admin/keys takes.
type addKeyBody struct {
	Name               string            `json:"name"`
	Key                string            `json:"key"`
	Tier               string            `json:"tier"`
	ExpiresAt          *string           `json:"expires_at"`
	UsageLimit         *int              `json:"usage_limit"`
	UsageWindowSeconds *int              `json:"usage_window_seconds"`
	Metadata           map[string]any    `json:"metadata"`
	Secrets            map[string]string `json:"secrets"`
}

// decodeAddKey reads the request, requires name, key and tier, and parses the
// optional expiry.
func decodeAddKey(w http.ResponseWriter, r *http.Request) (addKeyBody, *time.Time, bool) {
	var body addKeyBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return body, nil, false
	}
	if body.Name == "" || body.Key == "" || body.Tier == "" {
		writeError(w, http.StatusBadRequest, "name, key, and tier are required")
		return body, nil, false
	}
	if body.ExpiresAt == nil || *body.ExpiresAt == "" {
		return body, nil, true
	}
	expiresAt, err := time.Parse(time.RFC3339, *body.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "expires_at must be RFC3339")
		return body, nil, false
	}
	return body, &expiresAt, true
}

// sealedRows builds the key row and its secret rows, each value sealed (encrypted
// iff encryption is enabled) before it is stored.
func (s *Server) sealedRows(body addKeyBody, tierID string, expiresAt *time.Time) (*db.Key, []*db.KeySecret, error) {
	sealedKey, err := s.Sealer.Seal(body.Key)
	if err != nil {
		return nil, nil, err
	}
	metadata := body.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	key := &db.Key{
		ID:                 uuid.New().String(),
		Name:               body.Name,
		KeyValue:           sealedKey,
		TierID:             tierID,
		IsActive:           true,
		ExpiresAt:          expiresAt,
		UsageLimit:         body.UsageLimit,
		UsageWindowSeconds: body.UsageWindowSeconds,
		Metadata:           metadata,
	}
	secrets := make([]*db.KeySecret, 0, len(body.Secrets))
	for name, value := range body.Secrets {
		sealed, err := s.Sealer.Seal(value)
		if err != nil {
			return nil, nil, err
		}
		secrets = append(secrets, &db.KeySecret{KeyID: key.ID, Name: name, Value: sealed})
	}
	return key, secrets, nil
}

// AddKey handles POST /admin/keys
func (s *Server) AddKey(w http.ResponseWriter, r *http.Request) {
	body, expiresAt, ok := decodeAddKey(w, r)
	if !ok {
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
	key, secrets, err := s.sealedRows(body, tier.ID, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal key")
		return
	}
	if err := s.DB.CreateKey(ctx, key, secrets); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}
	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Msg("failed to reload key pool after adding key")
	}

	secretNames := make([]string, 0, len(secrets))
	for _, sec := range secrets {
		secretNames = append(secretNames, sec.Name)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                   key.ID,
		"name":                 key.Name,
		"tier":                 body.Tier,
		"expires_at":           body.ExpiresAt,
		"usage_limit":          body.UsageLimit,
		"usage_window_seconds": body.UsageWindowSeconds,
		"metadata":             key.Metadata,
		"secret_names":         secretNames,
	})
}

// ListKeys handles GET /admin/keys
func (s *Server) ListKeys(w http.ResponseWriter, r *http.Request) {
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
		result[i] = map[string]any{
			"id":              ks.ID,
			"name":            ks.Name,
			"tier_id":         ks.TierID,
			"is_active":       ks.IsActive,
			"expires_at":      rfc3339OrNil(ks.ExpiresAt),
			"exhausted_until": rfc3339OrNil(ks.ExhaustedUntil),
			"usage_limit":     ks.UsageLimit,
			"usage_count":     ks.UsageCount,
			"metadata":        metadata,
			"secret_names":    secretNames,
			"usage":           usage,
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// ExhaustKey handles POST /key/{id}/exhausted with body {"until": RFC3339}.
// A consumer reports a key the provider refused for the rest of its billing
// period; the pool stops serving it until then and resumes on its own. Auth is
// admin-OR-consumer like GetKey; a consumer may only report keys in its scope.
func (s *Server) ExhaustKey(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.resolveKeyCaller(w, r)
	if !ok {
		return
	}
	id := r.PathValue(pathID)
	var body struct {
		Until string `json:"until"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	until, err := time.Parse(time.RFC3339, body.Until)
	if err != nil {
		writeError(w, http.StatusBadRequest, "until must be RFC3339")
		return
	}
	tierID, found := s.Pool.TierOf(id)
	if !found {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if caller.allowedTierIDs != nil && !caller.allowedTierIDs[tierID] {
		writeError(w, http.StatusForbidden, "key is outside your scope")
		return
	}
	s.Pool.MarkExhausted(id, until)
	s.Usage.Event(db.NewUsageEvent(id, caller.consumerID, exhaustedEvent))
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "exhausted",
		"until":  until.UTC().Format(time.RFC3339),
	})
}

// DeleteKey handles DELETE /admin/keys/{id}
func (s *Server) DeleteKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.DB.DeleteKey(ctx, r.PathValue(pathID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}

	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Msg("failed to reload key pool after deleting key")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// rfc3339OrNil renders an optional time for JSON: the UTC timestamp, or null.
func rfc3339OrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
