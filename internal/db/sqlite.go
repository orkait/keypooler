package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

// SQLiteAdapter implements DBAdapter using SQLite
type SQLiteAdapter struct {
	db *sql.DB
}

// NewSQLiteAdapter creates a new SQLite database adapter
func NewSQLiteAdapter(dbPath string, maxOpenConns int, busyTimeoutMS int) (*SQLiteAdapter, error) {
	// Open database with busy_timeout and WAL mode for better read concurrency
	dsn := fmt.Sprintf("file:%s?_busy_timeout=%d&_journal_mode=WAL", dbPath, busyTimeoutMS)
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool — SQLite must use 1 open connection
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	// Test connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &SQLiteAdapter{db: sqlDB}, nil
}

// NewLibsqlAdapter opens a Turso/libSQL database over the network via the standard
// database/sql interface (pure Go, no CGO). dsn is the full libsql:// URL including
// ?authToken=. The SQL dialect is SQLite-compatible, so the same adapter methods and
// migrations run unchanged.
func NewLibsqlAdapter(dsn string, maxOpenConns int) (*SQLiteAdapter, error) {
	sqlDB, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open libsql database: %w", err)
	}

	// libSQL is a network DB (not a single-file writer lock), so a small pool is fine.
	if maxOpenConns < 1 {
		maxOpenConns = 1
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxOpenConns)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping libsql database: %w", err)
	}

	return &SQLiteAdapter{db: sqlDB}, nil
}

// DB returns the underlying *sql.DB for use with migrations
func (s *SQLiteAdapter) DB() *sql.DB {
	return s.db
}

// Close closes the database connection
func (s *SQLiteAdapter) Close() error {
	return s.db.Close()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
