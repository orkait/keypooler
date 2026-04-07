package scheduler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"key-pool-system/internal/contract"
	"key-pool-system/internal/db"
	"key-pool-system/internal/queue"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// ScheduleEntry represents a scheduled function execution.
type ScheduleEntry struct {
	ScriptName   string
	FunctionName string
	Feature      string
	CronExpr     string
	Input        string // JSON
	NextRunAt    time.Time
	IsActive     bool
}

// Scheduler manages cron-based execution scheduling.
type Scheduler struct {
	mu      sync.Mutex
	entries []*ScheduleEntry
	queue   *queue.Queue
	dbAdap  db.DBAdapter
	logger  zerolog.Logger
}

// New creates a scheduler.
func New(q *queue.Queue, dbAdap db.DBAdapter, logger zerolog.Logger) *Scheduler {
	return &Scheduler{
		queue:  q,
		dbAdap: dbAdap,
		logger: logger.With().Str("component", "scheduler").Logger(),
	}
}

// LoadFromDatabase builds schedule entries from active integration versions in the database.
func (s *Scheduler) LoadFromDatabase(ctx context.Context) error {
	versions, err := s.dbAdap.ListActiveIntegrationVersions(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = nil

	for _, version := range versions {
		fn, err := contract.ParseFunction([]byte(version.ContractJSON))
		if err != nil || !fn.Scheduling.Enabled {
			continue
		}

		inputJSON := "{}"
		if fn.Scheduling.Input != nil {
			b, err := json.Marshal(fn.Scheduling.Input)
			if err == nil {
				inputJSON = string(b)
			}
		}

		s.entries = append(s.entries, &ScheduleEntry{
			ScriptName:   version.IntegrationName,
			FunctionName: version.FunctionName,
			Feature:      version.Feature,
			CronExpr:     fn.Scheduling.Cron,
			Input:        inputJSON,
			NextRunAt:    nextCronTime(fn.Scheduling.Cron, time.Now()),
			IsActive:     true,
		})

		s.logger.Info().
			Str("integration", version.IntegrationName).
			Str("function", version.FunctionName).
			Str("cron", fn.Scheduling.Cron).
			Msg("scheduled integration version registered")
	}

	return nil
}

// Start begins the scheduler tick loop.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("scheduler stopped")
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// ActiveCount returns the number of active schedules.
func (s *Scheduler) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, e := range s.entries {
		if e.IsActive {
			count++
		}
	}
	return count
}

func (s *Scheduler) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, entry := range s.entries {
		if !entry.IsActive {
			continue
		}
		if now.Before(entry.NextRunAt) {
			continue
		}

		// Create execution
		execID := uuid.New().String()
		input := entry.Input
		exec := &db.Execution{
			ID:           execID,
			Script:       entry.ScriptName,
			FunctionName: entry.FunctionName,
			Status:       db.StatusPending,
			TriggerType:  db.TriggerSchedule,
			Input:        &input,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := s.dbAdap.CreateExecution(ctx, exec)
		cancel()

		if err != nil {
			s.logger.Error().Err(err).
				Str("script", entry.ScriptName).
				Str("function", entry.FunctionName).
				Msg("failed to create scheduled execution")
			continue
		}

		// Enqueue
		if err := s.queue.Enqueue(&queue.Item{
			ExecutionID: execID,
			Feature:     entry.Feature,
		}); err != nil {
			_ = s.dbAdap.UpdateExecutionResult(ctx, execID, db.StatusFailed, "", "failed to enqueue scheduled execution", time.Now().UTC())
			s.logger.Error().Err(err).
				Str("execution_id", execID).
				Msg("failed to enqueue scheduled execution")
			continue
		}

		s.logger.Info().
			Str("script", entry.ScriptName).
			Str("function", entry.FunctionName).
			Str("execution_id", execID).
			Msg("scheduled execution fired")

		// Advance to next run
		entry.NextRunAt = nextCronTime(entry.CronExpr, now)
	}
}

// nextCronTime parses simple cron minute expressions and returns the next run time.
// Supports: "*/N * * * *" (every N minutes) and "N * * * *" (at minute N of each hour).
// Falls back to every 1 minute for unsupported expressions.
func nextCronTime(expr string, from time.Time) time.Time {
	parts := strings.Fields(expr)
	if len(parts) < 5 {
		return from.Add(1 * time.Minute).Truncate(time.Minute)
	}

	minutePart := parts[0]
	base := from.Truncate(time.Minute).Add(time.Minute) // at least 1 min in the future

	// */N — every N minutes
	if strings.HasPrefix(minutePart, "*/") {
		n, err := strconv.Atoi(minutePart[2:])
		if err != nil || n <= 0 {
			return base
		}
		// Find next minute divisible by N
		m := base.Minute()
		next := m + (n - m%n)
		if m%n == 0 {
			next = m
		}
		if next >= 60 {
			next = 0
		}
		t := time.Date(base.Year(), base.Month(), base.Day(), base.Hour(), next, 0, 0, base.Location())
		if !t.After(from) {
			t = t.Add(time.Duration(n) * time.Minute)
		}
		return t
	}

	// N — at minute N of each hour
	if n, err := strconv.Atoi(minutePart); err == nil && n >= 0 && n < 60 {
		t := time.Date(base.Year(), base.Month(), base.Day(), base.Hour(), n, 0, 0, base.Location())
		if !t.After(from) {
			t = t.Add(time.Hour)
		}
		return t
	}

	return base
}
