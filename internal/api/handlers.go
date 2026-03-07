package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"key-pool-system/internal/config"
	"key-pool-system/internal/contract"
	"key-pool-system/internal/crypto"
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/queue"
	"key-pool-system/internal/scheduler"
	"key-pool-system/internal/util"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Server holds all dependencies needed by HTTP handlers.
type Server struct {
	DB        db.DBAdapter
	Queue     *queue.Queue
	Pool      *keypool.Manager
	Cfg       *config.Config
	Scheduler *scheduler.Scheduler
	Logger    zerolog.Logger

	mu        sync.RWMutex
	contracts map[string]*contract.Contract
}

// SetContracts replaces the contracts map (thread-safe).
func (s *Server) SetContracts(c map[string]*contract.Contract) {
	s.mu.Lock()
	s.contracts = c
	s.mu.Unlock()
}

// GetContracts returns the current contracts map (thread-safe).
func (s *Server) GetContracts() map[string]*contract.Contract {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contracts
}

// --- Public endpoints ---

func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Execute handles POST /api/execute
func (s *Server) Execute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Script      string         `json:"script"`
		Function    string         `json:"function"`
		Input       map[string]any `json:"input"`
		CallbackURL string         `json:"callback_url"`
	}

	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if body.Script == "" || body.Function == "" {
		writeError(w, http.StatusBadRequest, "script and function are required")
		return
	}

	// Validate script + function exist
	c, ok := s.GetContracts()[body.Script]
	if !ok {
		writeError(w, http.StatusNotFound, "script not found: "+body.Script)
		return
	}
	fn, ok := c.Functions[body.Function]
	if !ok {
		writeError(w, http.StatusNotFound, "function not found: "+body.Function)
		return
	}

	// Serialize input
	inputJSON := "{}"
	if body.Input != nil {
		b, _ := json.Marshal(body.Input)
		inputJSON = string(b)
	}

	// Create execution record
	execID := uuid.New().String()
	exec := &db.Execution{
		ID:           execID,
		Script:       body.Script,
		FunctionName: body.Function,
		Status:       db.StatusPending,
		TriggerType:  db.TriggerAPI,
		Input:        &inputJSON,
	}
	if body.CallbackURL != "" {
		exec.CallbackURL = &body.CallbackURL
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()
	if err := s.DB.CreateExecution(ctx, exec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create execution")
		return
	}

	// Enqueue
	if err := s.Queue.Enqueue(&queue.Item{
		ExecutionID: execID,
		Feature:     fn.Feature,
	}); err != nil {
		writeError(w, http.StatusServiceUnavailable, "queue is full")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"execution_id": execID,
		"status":       db.StatusPending,
	})
}

// GetExecution handles GET /api/executions/{id}
func (s *Server) GetExecution(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := extractPathParam(r.URL.Path, "/api/executions/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "execution id required")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	exec, err := s.DB.GetExecution(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if exec == nil {
		writeError(w, http.StatusNotFound, "execution not found")
		return
	}

	writeJSON(w, http.StatusOK, executionToResponse(exec))
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

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	// Check if tier name already exists
	existing, err := s.DB.GetTierByName(ctx, body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "tier already exists: "+body.Name)
		return
	}

	tier := &db.Tier{
		ID:   uuid.New().String(),
		Name: body.Name,
	}
	if err := s.DB.CreateTier(ctx, tier); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create tier")
		return
	}

	features := make([]*db.TierFeature, 0, len(body.Features))
	for feature, rate := range body.Features {
		features = append(features, &db.TierFeature{
			TierID:       tier.ID,
			Feature:      feature,
			RatePerMinute: rate,
		})
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

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

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
		result[i] = map[string]any{
			"id":       t.ID,
			"name":     t.Name,
			"features": fm,
		}
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

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	// Look up tier by name
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

	// Reload key pool
	_ = s.Pool.ReloadKeys()

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
			usage[feature] = map[string]any{
				"used":  info.Used,
				"limit": info.Limit,
			}
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

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	if err := s.DB.DeleteKey(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}

	_ = s.Pool.ReloadKeys()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Health handles GET /admin/health
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pool_size":        s.Pool.PoolSize(),
		"queue_size":       s.Queue.Size(),
		"active_schedules": s.Scheduler.ActiveCount(),
	})
}

// ScanScripts handles POST /admin/scripts/scan
func (s *Server) ScanScripts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	contracts, err := ScanScriptsDir(s.Cfg.ScriptsPath, s.Logger)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan failed: "+err.Error())
		return
	}

	// Update in-memory contracts
	s.SetContracts(contracts)
	s.Scheduler.LoadFromContracts(contracts)

	names := make([]string, 0, len(contracts))
	for name := range contracts {
		names = append(names, name)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scripts": names,
		"count":   len(contracts),
	})
}

// GetDeadLetters handles GET /admin/dead-letter
func (s *Server) GetDeadLetters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	dls, err := s.DB.GetDeadLetters(ctx, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	result := make([]map[string]any, len(dls))
	for i, dl := range dls {
		entry := map[string]any{
			"id":            dl.ID,
			"execution_id":  dl.ExecutionID,
			"script":        dl.Script,
			"function_name": dl.FunctionName,
			"attempts":      dl.Attempts,
			"failed_at":     dl.FailedAt,
		}
		if dl.Input != nil {
			entry["input"] = *dl.Input
		}
		if dl.Error != nil {
			entry["error"] = *dl.Error
		}
		result[i] = entry
	}

	writeJSON(w, http.StatusOK, result)
}

// RetryDeadLetter handles POST /admin/dead-letter/{id}/retry
func (s *Server) RetryDeadLetter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract ID — path is /admin/dead-letter/{id}/retry
	path := strings.TrimPrefix(r.URL.Path, "/admin/dead-letter/")
	id := strings.TrimSuffix(path, "/retry")
	if id == "" || id == path {
		writeError(w, http.StatusBadRequest, "dead letter id required")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	dl, err := s.DB.GetDeadLetter(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if dl == nil {
		writeError(w, http.StatusNotFound, "dead letter entry not found")
		return
	}

	// Look up contract to get feature
	c, ok := s.GetContracts()[dl.Script]
	if !ok {
		writeError(w, http.StatusNotFound, "script not found: "+dl.Script)
		return
	}
	fn, ok := c.Functions[dl.FunctionName]
	if !ok {
		writeError(w, http.StatusNotFound, "function not found: "+dl.FunctionName)
		return
	}

	// Create new execution
	execID := uuid.New().String()
	exec := &db.Execution{
		ID:           execID,
		Script:       dl.Script,
		FunctionName: dl.FunctionName,
		Status:       db.StatusPending,
		TriggerType:  db.TriggerAPI,
		Input:        dl.Input,
	}

	if err := s.DB.CreateExecution(ctx, exec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create execution")
		return
	}

	// Remove from dead letter
	_ = s.DB.DeleteDeadLetter(ctx, id)

	// Enqueue
	if err := s.Queue.Enqueue(&queue.Item{
		ExecutionID: execID,
		Feature:     fn.Feature,
	}); err != nil {
		writeError(w, http.StatusServiceUnavailable, "queue is full")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"execution_id": execID,
		"status":       db.StatusPending,
	})
}

// --- Helpers ---

func ScanScriptsDir(scriptsPath string, logger zerolog.Logger) (map[string]*contract.Contract, error) {
	contracts := make(map[string]*contract.Contract)

	entries, err := os.ReadDir(scriptsPath)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(scriptsPath, entry.Name())
		c, err := contract.LoadFromDir(dir)
		if err != nil {
			logger.Warn().Err(err).Str("dir", entry.Name()).Msg("skipping invalid script")
			continue
		}

		contracts[c.Name] = c
		logger.Info().Str("script", c.Name).Str("runtime", c.Runtime).
			Int("functions", len(c.Functions)).Msg("loaded script contract")
	}

	return contracts, nil
}

func executionToResponse(exec *db.Execution) map[string]any {
	resp := map[string]any{
		"id":            exec.ID,
		"script":        exec.Script,
		"function_name": exec.FunctionName,
		"status":        exec.Status,
		"trigger":       exec.TriggerType,
		"attempts":      exec.Attempts,
		"created_at":    exec.CreatedAt,
	}
	if exec.KeyID != nil {
		resp["key_id"] = *exec.KeyID
	}
	if exec.CallbackURL != nil {
		resp["callback_url"] = *exec.CallbackURL
	}
	if exec.Input != nil {
		resp["input"] = json.RawMessage(*exec.Input)
	}
	if exec.Output != nil {
		resp["output"] = json.RawMessage(*exec.Output)
	}
	if exec.Error != nil {
		resp["error"] = *exec.Error
	}
	if exec.CompletedAt != nil {
		resp["completed_at"] = *exec.CompletedAt
	}
	return resp
}

func extractPathParam(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(trimmed, "/"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}
