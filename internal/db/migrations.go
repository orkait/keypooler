package db

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationSuffix = ".sql"

// migration is one numbered SQL file.
type migration struct {
	version int
	name    string
	sql     string
}

// RunMigrations executes all pending migrations in order
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsPath string) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	migrations, err := loadMigrations(migrationsPath)
	if err != nil {
		return fmt.Errorf("failed to load migrations: %w", err)
	}

	applied, err := getAppliedMigrations(ctx, pool)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := executeMigration(ctx, pool, m); err != nil {
			return fmt.Errorf("failed to execute migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// loadMigrations reads every migration file in dir, in version order.
func loadMigrations(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), migrationSuffix) {
			continue
		}
		version, name, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, err
		}
		sql, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(sql)})
	}

	slices.SortFunc(migrations, func(a, b migration) int { return cmp.Compare(a.version, b.version) })
	return migrations, nil
}

// parseMigrationName reads "001_init.sql" as version 1 named "init".
func parseMigrationName(filename string) (int, string, error) {
	prefix, name, ok := strings.Cut(strings.TrimSuffix(filename, migrationSuffix), "_")
	version, err := strconv.Atoi(prefix)
	if !ok || err != nil || name == "" {
		return 0, "", fmt.Errorf("invalid migration filename %s (expected NNN_name%s)", filename, migrationSuffix)
	}
	return version, name, nil
}

// getAppliedMigrations returns a set of already applied migration versions
func getAppliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[int]bool, error) {
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, err
	}
	applied := make(map[int]bool, len(versions))
	for _, v := range versions {
		applied[v] = true
	}
	return applied, nil
}

// executeMigration runs a single migration within a transaction. The file may hold
// several statements, so it goes over the simple protocol, which takes them as one.
func executeMigration(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Conn().PgConn().Exec(ctx, m.sql).ReadAll(); err != nil {
		return fmt.Errorf("failed to execute SQL: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)",
		m.version, m.name,
	); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}
	return tx.Commit(ctx)
}
