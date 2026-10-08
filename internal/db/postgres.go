package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	minConns        = 1
	maxConnIdleTime = 5 * time.Minute
	connectTimeout  = 10 * time.Second
	uniqueViolation = "23505"
)

var ErrDuplicate = errors.New("already exists")

type PostgresAdapter struct {
	pool *pgxpool.Pool
}

func NewPostgresAdapter(dsn string, maxConns int) (*PostgresAdapter, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// The parse error can quote the URL, password included; never wrap it.
		return nil, fmt.Errorf("invalid DATABASE_URL")
	}
	cfg.MaxConns = int32(max(maxConns, 1))
	cfg.MinConns = minConns
	cfg.MaxConnIdleTime = maxConnIdleTime

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
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

func (a *PostgresAdapter) Pool() *pgxpool.Pool {
	return a.pool
}

func (a *PostgresAdapter) Close() error {
	a.pool.Close()
	return nil
}

func duplicate(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return ErrDuplicate
	}
	return err
}
