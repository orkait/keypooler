package api

import (
	"errors"
	"net/http"

	"github.com/orkait/keypooler/internal/db"

	"github.com/google/uuid"
)

// featureLimitBody is the per-feature rate-limit shape in tier requests and responses.
type featureLimitBody struct {
	RateLimit     int `json:"rate_limit"`
	WindowSeconds int `json:"window_seconds"`
}

// tierBody is what POST and PATCH /admin/tiers take. Description is a pointer so a
// PATCH can leave it untouched by omitting it.
type tierBody struct {
	Name        string                      `json:"name"`
	Description *string                     `json:"description"`
	Features    map[string]featureLimitBody `json:"features"`
}

// tierFeatures turns requested limits into the rows to store, filling the default
// window, and returns the limits as stored for the response.
func tierFeatures(tierID string, requested map[string]featureLimitBody) ([]*db.TierFeature, map[string]featureLimitBody) {
	stored := make([]*db.TierFeature, 0, len(requested))
	echoed := make(map[string]featureLimitBody, len(requested))
	for feature, limit := range requested {
		if limit.WindowSeconds <= 0 {
			limit.WindowSeconds = defaultWindowSeconds
		}
		stored = append(stored, &db.TierFeature{
			TierID:        tierID,
			Feature:       feature,
			RateLimit:     limit.RateLimit,
			WindowSeconds: limit.WindowSeconds,
		})
		echoed[feature] = limit
	}
	return stored, echoed
}

func tierResponse(tier *db.Tier, features map[string]featureLimitBody) map[string]any {
	return map[string]any{
		"id":          tier.ID,
		"name":        tier.Name,
		"description": tier.Description,
		"features":    features,
	}
}

// decodeTierBody reads a tier request and requires a name and at least one feature.
func decodeTierBody(w http.ResponseWriter, r *http.Request) (tierBody, bool) {
	var body tierBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return body, false
	}
	if body.Name == "" || len(body.Features) == 0 {
		writeError(w, http.StatusBadRequest, "name and features are required")
		return body, false
	}
	return body, true
}

// CreateTier handles POST /admin/tiers
func (s *Server) CreateTier(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeTierBody(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	tier := &db.Tier{ID: uuid.New().String(), Name: body.Name}
	if body.Description != nil {
		tier.Description = *body.Description
	}
	if err := s.DB.CreateTier(ctx, tier); errors.Is(err, db.ErrDuplicate) {
		writeError(w, http.StatusConflict, "tier already exists: "+body.Name)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create tier")
		return
	}

	stored, echoed := tierFeatures(tier.ID, body.Features)
	if err := s.DB.SetTierFeatures(ctx, tier.ID, stored); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set features")
		return
	}
	writeJSON(w, http.StatusCreated, tierResponse(tier, echoed))
}

// UpdateTierFeatures replaces the feature set (rate_limit + window_seconds) of an
// existing tier, then reloads the pool so the new limits take effect immediately
// for keys already in that tier.
func (s *Server) UpdateTierFeatures(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeTierBody(w, r)
	if !ok {
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

	if body.Description != nil {
		if err := s.DB.UpdateTierDescription(ctx, tier.ID, *body.Description); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update description")
			return
		}
		tier.Description = *body.Description
	}

	stored, echoed := tierFeatures(tier.ID, body.Features)
	if err := s.DB.SetTierFeatures(ctx, tier.ID, stored); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set features")
		return
	}
	if err := s.Pool.ReloadKeys(); err != nil {
		s.Logger.Error().Err(err).Str("tier", body.Name).Msg("failed to reload pool after tier update")
	}
	writeJSON(w, http.StatusOK, tierResponse(tier, echoed))
}

// ListTiers handles GET /admin/tiers
func (s *Server) ListTiers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tiers, err := s.DB.GetAllTiers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	byTier, err := s.DB.TierFeaturesByTier(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	result := make([]map[string]any, len(tiers))
	for i, t := range tiers {
		features := byTier[t.ID]
		limits := make(map[string]featureLimitBody, len(features))
		for _, f := range features {
			limits[f.Feature] = featureLimitBody{RateLimit: f.RateLimit, WindowSeconds: f.WindowSeconds}
		}
		result[i] = tierResponse(t, limits)
	}
	writeJSON(w, http.StatusOK, result)
}
