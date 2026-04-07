package db

import (
	"context"
	"path/filepath"
	"testing"
)

func setupSQLiteAdapter(t *testing.T) *SQLiteAdapter {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "keypooler.db")
	adapter, err := NewSQLiteAdapter(dbPath, 1, 5000)
	if err != nil {
		t.Fatalf("NewSQLiteAdapter() error = %v", err)
	}
	t.Cleanup(func() {
		_ = adapter.Close()
	})

	migrationsPath := filepath.Join("..", "..", "migrations")
	if err := RunMigrations(context.Background(), adapter.DB(), migrationsPath); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	return adapter
}

func TestIntegrationVersionLifecycle(t *testing.T) {
	adapter := setupSQLiteAdapter(t)
	ctx := context.Background()

	v1, err := adapter.CreateIntegrationVersion(ctx, &IntegrationVersion{
		IntegrationName: "openai-image",
		FunctionName:    "generate",
		Runtime:         "python",
		Feature:         "image-generation",
		ContractJSON:    `{"timeout":"15s","retry":{"enabled":true,"max_attempts":2},"scheduling":{"enabled":false}}`,
		Code:            `print("v1")`,
		CreatedBy:       "superclaw",
	})
	if err != nil {
		t.Fatalf("CreateIntegrationVersion(v1) error = %v", err)
	}
	if v1.Version != 1 {
		t.Fatalf("expected v1.Version = 1, got %d", v1.Version)
	}

	v2, err := adapter.CreateIntegrationVersion(ctx, &IntegrationVersion{
		IntegrationName: "openai-image",
		FunctionName:    "generate",
		Runtime:         "python",
		Feature:         "image-generation",
		ContractJSON:    `{"timeout":"20s","retry":{"enabled":true,"max_attempts":3},"scheduling":{"enabled":false}}`,
		Code:            `print("v2")`,
		CreatedBy:       "superclaw",
	})
	if err != nil {
		t.Fatalf("CreateIntegrationVersion(v2) error = %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("expected v2.Version = 2, got %d", v2.Version)
	}

	if err := adapter.ActivateIntegrationVersion(ctx, v1.ID); err != nil {
		t.Fatalf("ActivateIntegrationVersion(v1) error = %v", err)
	}

	active, err := adapter.GetActiveIntegrationVersion(ctx, "openai-image", "generate")
	if err != nil {
		t.Fatalf("GetActiveIntegrationVersion(v1) error = %v", err)
	}
	if active == nil || active.ID != v1.ID {
		t.Fatalf("expected active version %q, got %#v", v1.ID, active)
	}

	if err := adapter.ActivateIntegrationVersion(ctx, v2.ID); err != nil {
		t.Fatalf("ActivateIntegrationVersion(v2) error = %v", err)
	}

	active, err = adapter.GetActiveIntegrationVersion(ctx, "openai-image", "generate")
	if err != nil {
		t.Fatalf("GetActiveIntegrationVersion(v2) error = %v", err)
	}
	if active == nil || active.ID != v2.ID {
		t.Fatalf("expected active version %q, got %#v", v2.ID, active)
	}

	versions, err := adapter.ListIntegrationVersions(ctx, "openai-image", "generate")
	if err != nil {
		t.Fatalf("ListIntegrationVersions() error = %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("expected versions ordered [2,1], got [%d,%d]", versions[0].Version, versions[1].Version)
	}
	if versions[0].Status != IntegrationVersionStatusActive {
		t.Fatalf("expected newest version active, got %q", versions[0].Status)
	}
	if versions[1].Status != IntegrationVersionStatusDisabled {
		t.Fatalf("expected older version disabled, got %q", versions[1].Status)
	}
}
