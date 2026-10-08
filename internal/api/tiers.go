package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/orkait/keypooler/internal/db"

	"github.com/google/uuid"
)

type featureLimitBody struct {
	RateLimit     int `json:"rate_limit"`
	WindowSeconds int `json:"window_seconds"`
}

type tierBody struct {
	Name        string                      `json:"name"`
	Description *string                     `json:"description"`
	Features    map[string]featureLimitBody `json:"features"`
}

func tierFeatures(tierID string, requested map[string]featureLimitBody) ([]*db.TierFeature, map[string]featureLimitBody) {
	stored := make([]*db.TierFeature, 0, len(requested))
	echoed := make(map[string]featureLimitBody, len(requested))
	for feature, limit := range requested {
		if limit.WindowSeconds <= 0 {
			limit.WindowSeconds = defaultWindowSeconds
		}
		stored = append(stored, &db.TierFeature{TierID: tierID, Feature: feature, RateLimit: limit.RateLimit, WindowSeconds: limit.WindowSeconds})
		echoed[feature] = limit
	}
	return stored, echoed
}

func tierResponse(tier *db.Tier, features map[string]featureLimitBody) tierView {
	return tierView{ID: tier.ID, Name: tier.Name, Description: tier.Description, Features: features}
}

func decodeTierBody(w http.ResponseWriter, r *http.Request) (tierBody, bool) {
	var body tierBody
	if !decodeBody(w, r, &body) {
		return body, false
	}
	if body.Name == "" || len(body.Features) == 0 {
		writeError(w, http.StatusBadRequest, "name and features are required")
		return body, false
	}
	return body, true
}

func (s *Server) tierNamed(ctx context.Context, w http.ResponseWriter, name string) (*db.Tier, bool) {
	tier, err := s.DB.GetTierByName(ctx, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, msgDatabaseError)
		return nil, false
	}
	if tier == nil {
		writeError(w, http.StatusNotFound, msgTierNotFound+name)
		return nil, false
	}
	return tier, true
}

func (s *Server) CreateTier(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeTierBody(w, r)
	if !ok {
		return
	}
	tier := &db.Tier{ID: uuid.New().String(), Name: body.Name}
	if body.Description != nil {
		tier.Description = *body.Description
	}
	stored, echoed := tierFeatures(tier.ID, body.Features)
	if err := s.DB.CreateTier(r.Context(), tier, stored); errors.Is(err, db.ErrDuplicate) {
		writeError(w, http.StatusConflict, "tier already exists: "+body.Name)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create tier")
		return
	}
	writeJSON(w, http.StatusCreated, tierResponse(tier, echoed))
}

func (s *Server) UpdateTierFeatures(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeTierBody(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tier, ok := s.tierNamed(ctx, w, body.Name)
	if !ok {
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
	s.reload("tier update")
	writeJSON(w, http.StatusOK, tierResponse(tier, echoed))
}

func (s *Server) DeleteTier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue(pathName)
	tier, ok := s.tierNamed(ctx, w, name)
	if !ok {
		return
	}
	switch err := s.DB.DeleteTier(ctx, tier.ID); {
	case errors.Is(err, db.ErrTierInUse):
		writeError(w, http.StatusConflict, "tier still has keys; delete them first")
		return
	case errors.Is(err, db.ErrTierNotFound):
		writeError(w, http.StatusNotFound, msgTierNotFound+name)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to delete tier")
		return
	}
	s.reload("deleting tier")
	writeJSON(w, http.StatusOK, statusBody{Status: statusDeleted})
}

func (s *Server) ListTiers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tiers, err := s.DB.GetAllTiers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, msgDatabaseError)
		return
	}
	byTier, err := s.DB.TierFeaturesByTier(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, msgDatabaseError)
		return
	}
	result := make([]tierView, len(tiers))
	for i, t := range tiers {
		limits := make(map[string]featureLimitBody, len(byTier[t.ID]))
		for _, f := range byTier[t.ID] {
			limits[f.Feature] = featureLimitBody{RateLimit: f.RateLimit, WindowSeconds: f.WindowSeconds}
		}
		result[i] = tierResponse(t, limits)
	}
	writeJSON(w, http.StatusOK, result)
}
