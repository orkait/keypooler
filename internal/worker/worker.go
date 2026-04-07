package worker

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"time"

	"key-pool-system/internal/contract"
	"key-pool-system/internal/crypto"
	"key-pool-system/internal/db"
	"key-pool-system/internal/executor"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/queue"
	"key-pool-system/internal/util"
	"key-pool-system/internal/webhook"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Worker processes queue items by executing scripts.
type Worker struct {
	id       int
	queue    *queue.Queue
	pool     *keypool.Manager
	dbAdap   db.DBAdapter
	executor *executor.Client
	encKey   string
	logger   zerolog.Logger
}

// NewWorker creates a worker with all dependencies.
func NewWorker(
	id int,
	q *queue.Queue,
	pool *keypool.Manager,
	dbAdap db.DBAdapter,
	exec *executor.Client,
	encKey string,
	logger zerolog.Logger,
) *Worker {
	return &Worker{
		id:       id,
		queue:    q,
		pool:     pool,
		dbAdap:   dbAdap,
		executor: exec,
		encKey:   encKey,
		logger:   logger.With().Int("worker_id", id).Logger(),
	}
}

// Start begins the worker loop.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Debug().Msg("worker started")

	for {
		select {
		case <-ctx.Done():
			w.logger.Debug().Msg("worker stopped")
			return
		case item, ok := <-w.queue.Dequeue():
			if !ok {
				w.logger.Debug().Msg("queue closed, worker stopping")
				return
			}
			w.processItem(ctx, item)
		}
	}
}

func (w *Worker) processItem(ctx context.Context, item *queue.Item) {
	log := w.logger.With().Str("execution_id", item.ExecutionID).Logger()

	// 1. Load execution from DB
	dbCtx, cancel := util.DBContext(ctx, util.DBTimeoutLong)
	exec, err := w.dbAdap.GetExecution(dbCtx, item.ExecutionID)
	cancel()

	if err != nil {
		log.Error().Err(err).Msg("failed to load execution")
		return
	}
	if exec == nil {
		log.Warn().Msg("execution not found, skipping")
		return
	}
	if exec.Status == db.StatusSuccess || exec.Status == db.StatusFailed {
		return
	}

	// 2. Resolve exact integration version
	var (
		version *db.IntegrationVersion
		fn      *contract.Function
	)
	if exec.VersionID != nil && *exec.VersionID != "" {
		version, err = w.dbAdap.GetIntegrationVersion(ctx, *exec.VersionID)
	} else {
		version, err = w.dbAdap.GetActiveIntegrationVersion(ctx, exec.Script, exec.FunctionName)
	}
	if err != nil {
		log.Error().Err(err).Msg("failed to resolve integration version")
		w.failExecution(ctx, exec, "failed to resolve integration version")
		return
	}
	if version == nil {
		log.Error().Str("script", exec.Script).Str("function", exec.FunctionName).Msg("integration version not found")
		w.failExecution(ctx, exec, "integration version not found")
		return
	}
	fn, err = contract.ParseFunction([]byte(version.ContractJSON))
	if err != nil {
		log.Error().Err(err).Msg("invalid integration contract")
		w.failExecution(ctx, exec, "invalid integration contract: "+err.Error())
		return
	}

	// 3. Get a key for the feature
	key := w.pool.GetKeyForFeature(item.Feature)
	if key == nil {
		log.Warn().Str("feature", item.Feature).Msg("no key available, re-queuing")
		w.requeueWithDelay(ctx, item, exec.Attempts)
		return
	}

	// 4. Mark as running
	dbCtx2, cancel2 := util.DBContext(ctx, util.DBTimeoutShort)
	_ = w.dbAdap.UpdateExecutionStatus(dbCtx2, exec.ID, db.StatusRunning, key.ID, exec.Attempts+1)
	cancel2()

	// 5. Decrypt key
	decryptedKey, err := crypto.Decrypt(key.KeyEncrypted, w.encKey)
	if err != nil {
		log.Error().Err(err).Str("key_id", key.ID).Msg("failed to decrypt key")
		w.handleFailure(ctx, item, exec, fn, "decryption failed: "+err.Error())
		return
	}

	// 6. Execute script
	inputJSON := "{}"
	if exec.Input != nil {
		inputJSON = *exec.Input
	}

	result, err := w.executor.ExecuteWithID(ctx, exec.ID, version, fn, decryptedKey, inputJSON)
	if err != nil {
		log.Warn().Err(err).Msg("script execution failed")
		w.handleFailure(ctx, item, exec, fn, err.Error())
		return
	}

	// 7. Handle result
	if result.Success {
		w.handleSuccess(ctx, exec, result)
	} else {
		w.handleFailure(ctx, item, exec, fn, result.Error)
	}
}

func (w *Worker) handleSuccess(ctx context.Context, exec *db.Execution, result *executor.Result) {
	output := string(result.Data)

	dbCtx, cancel := util.DBContext(ctx, util.DBTimeoutShort)
	defer cancel()
	_ = w.dbAdap.UpdateExecutionResult(dbCtx, exec.ID, db.StatusSuccess, output, "", time.Now().UTC())

	// Fire webhook if callback URL is set
	if exec.CallbackURL != nil && *exec.CallbackURL != "" {
		payload := map[string]any{
			"execution_id": exec.ID,
			"status":       db.StatusSuccess,
			"output":       json.RawMessage(output),
		}
		webhook.Send(ctx, *exec.CallbackURL, payload, w.logger)
	}
}

func (w *Worker) handleFailure(ctx context.Context, item *queue.Item, exec *db.Execution, fn *contract.Function, errMsg string) {
	attempts := exec.Attempts + 1

	if fn.Retry.Enabled && attempts < fn.Retry.MaxAttempts {
		w.logger.Info().
			Str("execution_id", exec.ID).
			Int("attempt", attempts).
			Msg("scheduling retry")

		// Mark as retrying
		dbCtx, cancel := util.DBContext(ctx, util.DBTimeoutShort)
		_ = w.dbAdap.UpdateExecutionStatus(dbCtx, exec.ID, db.StatusRetrying, "", attempts)
		cancel()

		delay := calculateBackoff(attempts - 1)
		w.delayedEnqueue(ctx, item, delay)
	} else {
		w.logger.Warn().
			Str("execution_id", exec.ID).
			Int("attempts", attempts).
			Msg("execution permanently failed")

		dbCtx, cancel := util.DBContext(ctx, util.DBTimeoutShort)
		_ = w.dbAdap.UpdateExecutionResult(dbCtx, exec.ID, db.StatusFailed, "", errMsg, time.Now().UTC())
		cancel()

		// Insert into dead letter
		dl := &db.DeadLetter{
			ID:           uuid.New().String(),
			ExecutionID:  exec.ID,
			Script:       exec.Script,
			FunctionName: exec.FunctionName,
			VersionID:    exec.VersionID,
			Input:        exec.Input,
			Error:        &errMsg,
			Attempts:     attempts,
		}
		dbCtx2, cancel2 := util.DBContext(ctx, util.DBTimeoutShort)
		_ = w.dbAdap.CreateDeadLetter(dbCtx2, dl)
		cancel2()

		// Fire webhook on final failure
		if exec.CallbackURL != nil && *exec.CallbackURL != "" {
			payload := map[string]any{
				"execution_id": exec.ID,
				"status":       db.StatusFailed,
				"error":        errMsg,
			}
			webhook.Send(ctx, *exec.CallbackURL, payload, w.logger)
		}
	}
}

func (w *Worker) failExecution(ctx context.Context, exec *db.Execution, errMsg string) {
	dbCtx, cancel := util.DBContext(ctx, util.DBTimeoutShort)
	defer cancel()
	_ = w.dbAdap.UpdateExecutionResult(dbCtx, exec.ID, db.StatusFailed, "", errMsg, time.Now().UTC())
}

func (w *Worker) requeueWithDelay(ctx context.Context, item *queue.Item, attempts int) {
	delay := calculateBackoff(attempts)
	w.delayedEnqueue(ctx, item, delay)
}

func (w *Worker) delayedEnqueue(ctx context.Context, item *queue.Item, delay time.Duration) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := w.queue.Enqueue(item); err != nil {
				w.logger.Error().Err(err).
					Str("execution_id", item.ExecutionID).
					Msg("failed to re-queue")
			}
		}
	}()
}

// calculateBackoff returns exponential backoff with jitter.
// min(1s * 2^attempt, 30s) + random jitter up to 500ms.
func calculateBackoff(attempt int) time.Duration {
	base := time.Second
	maxDelay := 30 * time.Second

	delay := base
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
			break
		}
	}

	jitter := time.Duration(rand.IntN(500)) * time.Millisecond
	return delay + jitter
}
