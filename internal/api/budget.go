package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"

	"github.com/google/uuid"
)

type budgetBody struct {
	Amount   float64 `json:"amount"`
	Unit     string  `json:"unit"`
	ResetDay *int    `json:"reset_day"`
}

func (b *budgetBody) budget() (*db.Budget, error) {
	if b == nil {
		return nil, nil
	}
	budget := db.Budget{Amount: b.Amount, Unit: b.Unit, ResetDay: b.ResetDay}
	if err := keypool.ValidateBudget(budget); err != nil {
		return nil, err
	}
	return &budget, nil
}

func budgetViewOf(b *db.Budget) *budgetView {
	if b == nil {
		return nil
	}
	return &budgetView{Amount: b.Amount, Unit: b.Unit, ResetDay: b.ResetDay}
}

func (s *Server) SetKeyBudget(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	raw, present := body[budgetField]
	if !present {
		writeError(w, http.StatusBadRequest, "budget is required; send null to clear it")
		return
	}
	var requested *budgetBody
	if err := json.Unmarshal(raw, &requested); err != nil {
		writeError(w, http.StatusBadRequest, "invalid budget: "+err.Error())
		return
	}
	budget, err := requested.budget()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue(pathID)
	err = s.DB.UpdateKeyBudget(r.Context(), id, budget)
	if errors.Is(err, db.ErrKeyNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set budget")
		return
	}
	s.reload("setting a key budget")
	writeJSON(w, http.StatusOK, budgetSet{KeyID: id, Budget: budgetViewOf(budget)})
}

type spendBody struct {
	Amount    float64 `json:"amount"`
	Unit      string  `json:"unit"`
	RequestID string  `json:"request_id"`
}

func (s *Server) ReportSpend(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.resolveKeyCaller(w, r)
	if !ok {
		return
	}
	var body spendBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if _, err := (&budgetBody{Amount: body.Amount, Unit: body.Unit}).budget(); err != nil || body.RequestID == "" {
		writeError(w, http.StatusBadRequest, "amount above 0, a known unit and a request_id are required")
		return
	}
	id := r.PathValue(pathID)
	tierID, found := s.Pool.TierOf(id)
	if !found {
		writeError(w, http.StatusNotFound, keypool.ErrUnknownKey.Error())
		return
	}
	if caller.allowedTierIDs != nil && !caller.allowedTierIDs[tierID] {
		writeError(w, http.StatusForbidden, "key is outside your scope")
		return
	}
	spend, err := s.Pool.RecordSpend(r.Context(), &db.SpendEvent{
		ID: uuid.New().String(), KeyID: id, ConsumerID: caller.consumerID,
		Amount: body.Amount, Unit: body.Unit, RequestID: body.RequestID, CreatedAt: time.Now(),
	})
	switch {
	case errors.Is(err, keypool.ErrUnknownKey):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, keypool.ErrUnitMismatch):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to record spend")
		return
	}
	writeJSON(w, http.StatusOK, spendRecorded{
		KeyID: spend.KeyID, Spent: spend.Spent, Budget: budgetViewOf(spend.Budget),
		Remaining: keypool.Remaining(spend.Budget, spend.Spent), ResetsAt: rfc3339OrNil(spend.ResetsAt), Duplicate: spend.Duplicate,
	})
}
