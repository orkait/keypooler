package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/orkait/keypooler/internal/db"

	"github.com/google/uuid"
)

func (s *Server) CreateConsumer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	buf := make([]byte, consumerTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	token := hex.EncodeToString(buf)
	consumer := &db.Consumer{
		ID:          uuid.New().String(),
		Name:        body.Name,
		TokenHash:   hashToken(token),
		Description: body.Description,
		IsActive:    true,
	}
	if err := s.DB.CreateConsumer(r.Context(), consumer); errors.Is(err, db.ErrDuplicate) {
		writeError(w, http.StatusConflict, "consumer already exists: "+body.Name)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create consumer")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": consumer.ID, "name": consumer.Name, "token": token})
}

func (s *Server) ListConsumers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	consumers, err := s.DB.GetAllConsumers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	tiers, err := s.DB.GetAllTiers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	byConsumer, err := s.DB.ConsumerScopesByConsumer(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	tierName := make(map[string]string, len(tiers))
	for _, t := range tiers {
		tierName[t.ID] = t.Name
	}

	result := make([]map[string]any, len(consumers))
	for i, c := range consumers {
		scopes := []string{}
		for _, id := range byConsumer[c.ID] {
			if name, ok := tierName[id]; ok {
				scopes = append(scopes, name)
			}
		}
		result[i] = map[string]any{
			"id":          c.ID,
			"name":        c.Name,
			"description": c.Description,
			"is_active":   c.IsActive,
			"scopes":      scopes,
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) DeleteConsumer(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteConsumer(r.Context(), r.PathValue(pathID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete consumer")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) AddConsumerScope(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathID)
	var body struct {
		Tier string `json:"tier"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Tier == "" {
		writeError(w, http.StatusBadRequest, "tier is required")
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
	if err := s.DB.AddConsumerScope(ctx, id, tier.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add scope")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"consumer_id": id, "tier": tier.Name})
}

func (s *Server) ListUsageEvents(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), defaultUsageListLimit, maxUsageListLimit)
	events, err := s.DB.ListUsageEvents(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	result := make([]map[string]any, len(events))
	for i, e := range events {
		result[i] = map[string]any{
			"key_id":      e.KeyID,
			"consumer_id": e.ConsumerID,
			"feature":     e.Feature,
			"created_at":  e.CreatedAt.UTC().Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, result)
}
