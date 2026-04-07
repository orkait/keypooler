package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"key-pool-system/internal/config"
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

func TestExecuteMarksExecutionFailedWhenQueueIsFull(t *testing.T) {
	adapter := setupSQLiteAdapter(t)
	ctx := context.Background()

	version, err := adapter.CreateIntegrationVersion(ctx, &db.IntegrationVersion{
		IntegrationName: "demo-service",
		FunctionName:    "generate",
		Runtime:         "python",
		Feature:         "image-generation",
		ContractJSON:    `{"timeout":"15s","retry":{"enabled":true,"max_attempts":2},"scheduling":{"enabled":false}}`,
		Code:            `print("hello")`,
		CreatedBy:       "superclaw",
	})
	if err != nil {
		t.Fatalf("CreateIntegrationVersion() error = %v", err)
	}
	if err := adapter.ActivateIntegrationVersion(ctx, version.ID); err != nil {
		t.Fatalf("ActivateIntegrationVersion() error = %v", err)
	}

	server := &Server{
		DB:     adapter,
		Queue:  queue.NewQueue(0),
		Cfg:    &config.Config{},
		Logger: zerolog.Nop(),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/execute", strings.NewReader(`{"integration":"demo-service","function":"generate","input":{"prompt":"cat"}}`))
	rec := httptest.NewRecorder()
	server.Execute(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rec.Code)
	}

	execs, err := adapter.GetExecutionsByStatus(ctx, db.StatusFailed, 10)
	if err != nil {
		t.Fatalf("GetExecutionsByStatus() error = %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("expected 1 failed execution, got %d", len(execs))
	}
	if execs[0].VersionID == nil || *execs[0].VersionID != version.ID {
		t.Fatalf("expected failed execution to reference version %q", version.ID)
	}
	if execs[0].Error == nil || *execs[0].Error != "failed to enqueue execution" {
		t.Fatalf("unexpected execution error: %#v", execs[0].Error)
	}
}
