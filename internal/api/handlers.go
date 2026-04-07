package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

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
		Script      string          `json:"script"`
		Integration string          `json:"integration"`
		Function    string          `json:"function"`
		Input       json.RawMessage `json:"input"`
		CallbackURL string          `json:"callback_url"`
	}

	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	integrationName := body.Integration
	if integrationName == "" {
		integrationName = body.Script
	}
	if integrationName == "" || body.Function == "" {
		writeError(w, http.StatusBadRequest, "integration/script and function are required")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	version, err := s.DB.GetActiveIntegrationVersion(ctx, integrationName, body.Function)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if version == nil {
		writeError(w, http.StatusNotFound, "active integration version not found")
		return
	}

	// Serialize input
	inputJSON := "{}"
	if len(body.Input) > 0 {
		inputJSON = string(body.Input)
	}

	// Create execution record
	execID := uuid.New().String()
	exec := &db.Execution{
		ID:           execID,
		Script:       integrationName,
		FunctionName: body.Function,
		VersionID:    &version.ID,
		Status:       db.StatusPending,
		TriggerType:  db.TriggerAPI,
		Input:        &inputJSON,
	}
	if body.CallbackURL != "" {
		exec.CallbackURL = &body.CallbackURL
	}

	if err := s.DB.CreateExecution(ctx, exec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create execution")
		return
	}

	// Enqueue
	if err := s.Queue.Enqueue(&queue.Item{
		ExecutionID: execID,
		Feature:     version.Feature,
	}); err != nil {
		_ = s.DB.UpdateExecutionResult(ctx, execID, db.StatusFailed, "", "failed to enqueue execution", utilNowUTC())
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

// CreateIntegrationVersion handles POST /admin/integrations
func (s *Server) CreateIntegrationVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Integration string          `json:"integration"`
		Function    string          `json:"function"`
		Runtime     string          `json:"runtime"`
		Feature     string          `json:"feature"`
		Contract    json.RawMessage `json:"contract"`
		Code        string          `json:"code"`
		CreatedBy   string          `json:"created_by"`
		Activate    bool            `json:"activate"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Integration == "" || body.Function == "" || body.Runtime == "" || body.Feature == "" || len(body.Contract) == 0 || body.Code == "" {
		writeError(w, http.StatusBadRequest, "integration, function, runtime, feature, contract, and code are required")
		return
	}
	runtimeName, err := contract.NormalizeRuntime(body.Runtime)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fn, err := contract.ParseFunction(body.Contract)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid contract: "+err.Error())
		return
	}
	if fn.Feature != "" && fn.Feature != body.Feature {
		writeError(w, http.StatusBadRequest, "contract feature must match top-level feature")
		return
	}
	if body.CreatedBy == "" {
		body.CreatedBy = "admin"
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutLong)
	defer cancel()

	version, err := s.DB.CreateIntegrationVersion(ctx, &db.IntegrationVersion{
		IntegrationName: body.Integration,
		FunctionName:    body.Function,
		Runtime:         runtimeName,
		Feature:         body.Feature,
		ContractJSON:    string(body.Contract),
		Code:            body.Code,
		CreatedBy:       body.CreatedBy,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create integration version")
		return
	}
	if body.Activate {
		if err := s.DB.ActivateIntegrationVersion(ctx, version.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to activate integration version")
			return
		}
		version.Status = db.IntegrationVersionStatusActive
	}
	_ = s.reloadSchedules(ctx)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          version.ID,
		"integration": version.IntegrationName,
		"function":    version.FunctionName,
		"version":     version.Version,
		"status":      version.Status,
	})
}

// ListIntegrations handles GET /admin/integrations
func (s *Server) ListIntegrations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	integrationName := r.URL.Query().Get("integration")
	functionName := r.URL.Query().Get("function")

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutShort)
	defer cancel()

	if integrationName != "" && functionName != "" {
		versions, err := s.DB.ListIntegrationVersions(ctx, integrationName, functionName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "database error")
			return
		}
		writeJSON(w, http.StatusOK, versions)
		return
	}

	versions, err := s.DB.ListActiveIntegrationVersions(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// ActivateIntegrationVersion handles POST /admin/integrations/versions/{id}/activate
func (s *Server) ActivateIntegrationVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/admin/integrations/versions/")
	id := strings.TrimSuffix(path, "/activate")
	if id == "" || id == path {
		writeError(w, http.StatusBadRequest, "integration version id required")
		return
	}

	ctx, cancel := util.DBContext(r.Context(), util.DBTimeoutLong)
	defer cancel()
	if err := s.DB.ActivateIntegrationVersion(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.reloadSchedules(ctx)
	writeJSON(w, http.StatusOK, map[string]string{"status": "activated"})
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
			TierID:        tier.ID,
			Feature:       feature,
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

	var version *db.IntegrationVersion
	if dl.VersionID != nil && *dl.VersionID != "" {
		version, err = s.DB.GetIntegrationVersion(ctx, *dl.VersionID)
	} else {
		version, err = s.DB.GetActiveIntegrationVersion(ctx, dl.Script, dl.FunctionName)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if version == nil {
		writeError(w, http.StatusNotFound, "integration version not found")
		return
	}

	// Create new execution
	execID := uuid.New().String()
	exec := &db.Execution{
		ID:           execID,
		Script:       dl.Script,
		FunctionName: dl.FunctionName,
		VersionID:    dl.VersionID,
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
		Feature:     version.Feature,
	}); err != nil {
		_ = s.DB.UpdateExecutionResult(ctx, execID, db.StatusFailed, "", "failed to enqueue execution", utilNowUTC())
		writeError(w, http.StatusServiceUnavailable, "queue is full")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"execution_id": execID,
		"status":       db.StatusPending,
	})
}

// --- Helpers ---

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
	if exec.VersionID != nil {
		resp["version_id"] = *exec.VersionID
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

func (s *Server) reloadSchedules(ctx context.Context) error {
	if s.Scheduler == nil {
		return nil
	}
	return s.Scheduler.LoadFromDatabase(ctx)
}

func utilNowUTC() time.Time {
	return time.Now().UTC()
}
