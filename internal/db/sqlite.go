package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
	_ "modernc.org/sqlite"
)

// SQLiteAdapter implements DBAdapter using SQLite
type SQLiteAdapter struct {
	db *sql.DB
}

// NewSQLiteAdapter creates a new SQLite database adapter
func NewSQLiteAdapter(dbPath string, maxOpenConns int, busyTimeoutMS int) (*SQLiteAdapter, error) {
	// Open database with busy_timeout and WAL mode for better read concurrency.
	// modernc.org/sqlite (pure Go, no CGO) takes pragmas via repeated _pragma= params.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)", dbPath, busyTimeoutMS)
	sqlDB, err := sql.Open("sqlite", dsn)
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
	// Parse first: a malformed dsn makes the driver's url.Parse error echo the raw
	// URL (with its embedded authToken). Guard so that string never reaches a %w wrap
	// or the logger.
	if _, perr := url.Parse(dsn); perr != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL")
	}

	sqlDB, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open libsql database")
	}

	// libSQL is a network DB (not a single-file writer lock), so a small pool is fine.
	if maxOpenConns < 1 {
		maxOpenConns = 1
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxOpenConns)
	// Remote streams: recycle before Turso server-side stream batons expire, and bound
	// idle streams so a long-idle connection isn't handed out stale.
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	sqlDB.SetConnMaxIdleTime(1 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		// A valid-URL ping error (dial/4xx/5xx) does not contain the token; safe to wrap.
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

// uuidString generates a random UUID for server-assigned row identifiers.
func uuidString() string {
	return uuid.New().String()
}
