package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"key-pool-system/internal/db"
	"key-pool-system/internal/queue"

	"github.com/rs/zerolog"
)

func setupSQLiteAdapter(t *testing.T) *db.SQLiteAdapter {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "keypooler.db")
	adapter, err := db.NewSQLiteAdapter(dbPath, 1, 5000)
	if err != nil {
		t.Fatalf("NewSQLiteAdapter() error = %v", err)
	}
	t.Cleanup(func() {
		_ = adapter.Close()
	})

	migrationsPath := filepath.Join("..", "..", "migrations")
	if err := db.RunMigrations(context.Background(), adapter.DB(), migrationsPath); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	return adapter
}

func TestSchedulerPinsVersionIDIntoExecution(t *testing.T) {
	adapter := setupSQLiteAdapter(t)
	ctx := context.Background()

	version, err := adapter.CreateIntegrationVersion(ctx, &db.IntegrationVersion{
		IntegrationName: "demo-service",
		FunctionName:    "generate",
		Runtime:         "python",
		Feature:         "image-generation",
		ContractJSON:    `{"timeout":"15s","retry":{"enabled":true,"max_attempts":2},"scheduling":{"enabled":true,"cron":"*/5 * * * *","input":{"prompt":"cat"}}}`,
		Code:            `print("hello")`,
		CreatedBy:       "superclaw",
	})
	if err != nil {
		t.Fatalf("CreateIntegrationVersion() error = %v", err)
	}
	if err := adapter.ActivateIntegrationVersion(ctx, version.ID); err != nil {
		t.Fatalf("ActivateIntegrationVersion() error = %v", err)
	}

	q := queue.NewQueue(10)
	s := New(q, adapter, zerolog.Nop())
	if err := s.LoadFromDatabase(ctx); err != nil {
		t.Fatalf("LoadFromDatabase() error = %v", err)
	}
	if len(s.entries) != 1 {
		t.Fatalf("expected 1 schedule entry, got %d", len(s.entries))
	}
	s.entries[0].NextRunAt = time.Now().Add(-time.Second)

	s.tick()

	execs, err := adapter.GetExecutionsByStatus(ctx, db.StatusPending, 10)
	if err != nil {
		t.Fatalf("GetExecutionsByStatus() error = %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("expected 1 pending execution, got %d", len(execs))
	}
	if execs[0].VersionID == nil || *execs[0].VersionID != version.ID {
		t.Fatalf("expected scheduled execution to pin version %q", version.ID)
	}
}

func TestSchedulerMarksExecutionFailedWhenQueueIsFull(t *testing.T) {
	adapter := setupSQLiteAdapter(t)
	ctx := context.Background()

	version, err := adapter.CreateIntegrationVersion(ctx, &db.IntegrationVersion{
		IntegrationName: "demo-service",
		FunctionName:    "generate",
		Runtime:         "python",
		Feature:         "image-generation",
		ContractJSON:    `{"timeout":"15s","retry":{"enabled":true,"max_attempts":2},"scheduling":{"enabled":true,"cron":"*/5 * * * *","input":{"prompt":"cat"}}}`,
		Code:            `print("hello")`,
		CreatedBy:       "superclaw",
	})
	if err != nil {
		t.Fatalf("CreateIntegrationVersion() error = %v", err)
	}
	if err := adapter.ActivateIntegrationVersion(ctx, version.ID); err != nil {
		t.Fatalf("ActivateIntegrationVersion() error = %v", err)
	}

	q := queue.NewQueue(0)
	s := New(q, adapter, zerolog.Nop())
	if err := s.LoadFromDatabase(ctx); err != nil {
		t.Fatalf("LoadFromDatabase() error = %v", err)
	}
	s.entries[0].NextRunAt = time.Now().Add(-time.Second)

	s.tick()

	execs, err := adapter.GetExecutionsByStatus(ctx, db.StatusFailed, 10)
	if err != nil {
		t.Fatalf("GetExecutionsByStatus() error = %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("expected 1 failed execution, got %d", len(execs))
	}
	if execs[0].Error == nil || *execs[0].Error != "failed to enqueue scheduled execution" {
		t.Fatalf("unexpected error: %#v", execs[0].Error)
	}
}
