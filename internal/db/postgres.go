package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresAdapter implements DBAdapter on a native pgx pool: the binary protocol and
// a per-connection prepared-statement cache, so a repeated query is one round trip.
type PostgresAdapter struct {
	pool *pgxpool.Pool
}

// NewPostgresAdapter opens a pool on a postgres:// URL and checks it answers.
func NewPostgresAdapter(dsn string, maxConns int) (*PostgresAdapter, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// The parse error can quote the URL, password included; never wrap it.
		return nil, fmt.Errorf("invalid DATABASE_URL")
	}
	if maxConns < 1 {
		maxConns = 1
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open database pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return &PostgresAdapter{pool: pool}, nil
}

// Pool returns the underlying pool, for migrations.
func (a *PostgresAdapter) Pool() *pgxpool.Pool {
	return a.pool
}

// Close closes every pooled connection.
func (a *PostgresAdapter) Close() error {
	a.pool.Close()
	return nil
}

// notFound reports pgx.ErrNoRows as the caller's own "not found" error.
func notFound(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s not found", what)
	}
	return err
}

// uuidString generates a random UUID for server-assigned row identifiers.
func uuidString() string {
	return uuid.New().String()
}
