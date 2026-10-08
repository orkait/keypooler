package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"

	"github.com/google/uuid"
)

func (s *Server) GetKey(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.resolveKeyCaller(w, r)
	if !ok {
		return
	}
	feature := r.URL.Query().Get(featureParam)
	if feature == "" {
		writeError(w, http.StatusBadRequest, "feature query param required")
		return
	}
	key, err := s.Pool.GetKeyForFeature(feature, caller.allowedTierIDs)
	if errors.Is(err, keypool.ErrOutOfScope) && caller.allowedTierIDs != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusTooManyRequests, keypool.ErrExhausted.Error())
		return
	}
	value, err := s.Sealer.Open(key.KeyValue)
	if err != nil {
		s.Logger.Error().Err(err).Str("key_id", key.ID).Msg("failed to open key value")
		writeError(w, http.StatusInternalServerError, "failed to read key")
		return
	}
	s.Usage.Event(db.NewUsageEvent(key.ID, caller.consumerID, feature))
	writeJSON(w, http.StatusOK, drawnKey{KeyID: key.ID, Value: value, Metadata: key.Metadata, Secrets: key.Secrets})
}

type addKeyBody struct {
	Name               string            `json:"name"`
	Key                string            `json:"key"`
	Tier               string            `json:"tier"`
	ExpiresAt          *string           `json:"expires_at"`
	UsageLimit         *int              `json:"usage_limit"`
	UsageWindowSeconds *int              `json:"usage_window_seconds"`
	Metadata           map[string]any    `json:"metadata"`
	Secrets            map[string]string `json:"secrets"`
	Budget             *budgetBody       `json:"budget"`
}

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
	if _, err := body.Budget.budget(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

func (s *Server) sealedRows(body addKeyBody, tierID string, expiresAt *time.Time) (*db.Key, []*db.KeySecret, error) {
	sealedKey, err := s.Sealer.Seal(body.Key)
	if err != nil {
		return nil, nil, err
	}
	budget, err := body.Budget.budget()
	if err != nil {
		return nil, nil, err
	}
	key := &db.Key{
		Budget:             budget,
		ID:                 uuid.New().String(),
		Name:               body.Name,
		KeyValue:           sealedKey,
		TierID:             tierID,
		IsActive:           true,
		ExpiresAt:          expiresAt,
		UsageLimit:         body.UsageLimit,
		UsageWindowSeconds: body.UsageWindowSeconds,
		Metadata:           body.Metadata,
	}
	if key.Metadata == nil {
		key.Metadata = map[string]any{}
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
	s.reload("adding key")
	names := make([]string, 0, len(secrets))
	for _, sec := range secrets {
		names = append(names, sec.Name)
	}
	writeJSON(w, http.StatusCreated, addedKey{
		ID:                 key.ID,
		Name:               key.Name,
		Tier:               body.Tier,
		ExpiresAt:          body.ExpiresAt,
		UsageLimit:         body.UsageLimit,
		UsageWindowSeconds: body.UsageWindowSeconds,
		Metadata:           key.Metadata,
		SecretNames:        names,
	})
}

func (s *Server) reload(after string) {
	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Msg("failed to reload key pool after " + after)
	}
}

func (s *Server) ListKeys(w http.ResponseWriter, r *http.Request) {
	statuses := s.Pool.GetHealthStatus()
	listed := make([]listedKey, len(statuses))
	for i, ks := range statuses {
		usage := make(map[string]rateUsage, len(ks.Usage))
		for feature, info := range ks.Usage {
			usage[feature] = rateUsage{Used: info.Used, Limit: info.Limit, WindowSeconds: info.WindowSeconds}
		}
		listed[i] = listedKey{
			ID:             ks.ID,
			Name:           ks.Name,
			TierID:         ks.TierID,
			IsActive:       ks.IsActive,
			ExpiresAt:      rfc3339OrNil(ks.ExpiresAt),
			ExhaustedUntil: rfc3339OrNil(ks.ExhaustedUntil),
			UsageLimit:     ks.UsageLimit,
			UsageCount:     ks.UsageCount,
			Budget:         budgetViewOf(ks.Budget),
			Spent:          ks.Spent,
			Remaining:      keypool.Remaining(ks.Budget, ks.Spent),
			ResetsAt:       rfc3339OrNil(ks.ResetsAt),
			Metadata:       ks.Metadata,
			SecretNames:    ks.SecretNames,
			Usage:          usage,
		}
	}
	writeJSON(w, http.StatusOK, listed)
}

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
	writeJSON(w, http.StatusOK, exhaustedKey{Status: statusExhausted, Until: rfc3339(until)})
}

func (s *Server) DeleteKey(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteKey(r.Context(), r.PathValue(pathID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}
	s.reload("deleting key")
	writeJSON(w, http.StatusOK, statusBody{Status: statusDeleted})
}
