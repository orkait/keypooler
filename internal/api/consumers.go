package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/orkait/keypooler/internal/db"

	"github.com/google/uuid"
)

func (s *Server) CreateConsumer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeBody(w, r, &body) {
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
	writeJSON(w, http.StatusCreated, createdConsumer{ID: consumer.ID, Name: consumer.Name, Token: token})
}

func (s *Server) ListConsumers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	consumers, err := s.DB.GetAllConsumers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, msgDatabaseError)
		return
	}
	tiers, err := s.DB.GetAllTiers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, msgDatabaseError)
		return
	}
	byConsumer, err := s.DB.ConsumerScopesByConsumer(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, msgDatabaseError)
		return
	}
	tierName := make(map[string]string, len(tiers))
	for _, t := range tiers {
		tierName[t.ID] = t.Name
	}
	listed := make([]listedConsumer, len(consumers))
	for i, c := range consumers {
		scopes := []string{}
		for _, id := range byConsumer[c.ID] {
			if name, ok := tierName[id]; ok {
				scopes = append(scopes, name)
			}
		}
		listed[i] = listedConsumer{ID: c.ID, Name: c.Name, Description: c.Description, IsActive: c.IsActive, Scopes: scopes}
	}
	writeJSON(w, http.StatusOK, listed)
}

func (s *Server) DeleteConsumer(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteConsumer(r.Context(), r.PathValue(pathID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete consumer")
		return
	}
	writeJSON(w, http.StatusOK, statusBody{Status: statusDeleted})
}

func (s *Server) AddConsumerScope(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathID)
	var body struct {
		Tier string `json:"tier"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Tier == "" {
		writeError(w, http.StatusBadRequest, "tier is required")
		return
	}
	ctx := r.Context()
	tier, ok := s.tierNamed(ctx, w, body.Tier)
	if !ok {
		return
	}
	if err := s.DB.AddConsumerScope(ctx, id, tier.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add scope")
		return
	}
	writeJSON(w, http.StatusCreated, grantedScope{ConsumerID: id, Tier: tier.Name})
}

func (s *Server) ListUsageEvents(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get(limitParam), defaultUsageListLimit, maxUsageListLimit)
	events, err := s.DB.ListUsageEvents(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, msgDatabaseError)
		return
	}
	listed := make([]usageEvent, len(events))
	for i, e := range events {
		listed[i] = usageEvent{KeyID: e.KeyID, ConsumerID: e.ConsumerID, Feature: e.Feature, CreatedAt: rfc3339(e.CreatedAt)}
	}
	writeJSON(w, http.StatusOK, listed)
}
