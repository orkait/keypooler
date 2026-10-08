package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrKeyNotFound  = errors.New("key not found")
	ErrTierNotFound = errors.New("tier not found")
	ErrTierInUse    = errors.New("tier still has keys")
)

const foreignKeyViolation = "23503"

func (a *PostgresAdapter) DeleteTier(ctx context.Context, id string) error {
	tag, err := a.pool.Exec(ctx, "DELETE FROM tiers WHERE id = $1", id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return ErrTierInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTierNotFound
	}
	return nil
}

func budgetColumns(b *Budget) (amount *float64, unit *string, resetDay *int) {
	if b == nil {
		return nil, nil, nil
	}
	return &b.Amount, &b.Unit, b.ResetDay
}

func budgetOf(amount *float64, unit *string, resetDay *int) *Budget {
	if amount == nil || unit == nil {
		return nil
	}
	return &Budget{Amount: *amount, Unit: *unit, ResetDay: resetDay}
}

func (a *PostgresAdapter) UpdateKeyBudget(ctx context.Context, keyID string, budget *Budget) error {
	amount, unit, resetDay := budgetColumns(budget)
	tag, err := a.pool.Exec(ctx, `UPDATE keys SET
		budget_amount = $2, budget_unit = $3, budget_reset_day = $4,
		spent = CASE WHEN budget_unit IS NOT DISTINCT FROM $3 THEN spent ELSE 0 END,
		spent_period_start = CASE WHEN budget_unit IS NOT DISTINCT FROM $3 THEN spent_period_start ELSE NULL END
		WHERE id = $1`, keyID, amount, unit, resetDay)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrKeyNotFound
	}
	return nil
}

func (a *PostgresAdapter) RecordSpend(ctx context.Context, e *SpendEvent, periodStart *time.Time) (float64, bool, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `INSERT INTO spend_events (id, key_id, consumer_id, amount, unit, request_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (request_id) DO NOTHING`,
		e.ID, e.KeyID, e.ConsumerID, e.Amount, e.Unit, e.RequestID, e.CreatedAt)
	if err != nil {
		return 0, false, err
	}
	duplicate := tag.RowsAffected() == 0
	var spent float64
	switch {
	case periodStart == nil:
	case duplicate:
		err = tx.QueryRow(ctx, `SELECT CASE WHEN spent_period_start = $2 THEN spent ELSE 0 END FROM keys WHERE id = $1`,
			e.KeyID, periodStart).Scan(&spent)
	default:
		err = tx.QueryRow(ctx, `UPDATE keys SET
			spent = CASE WHEN spent_period_start = $2 THEN spent + $3 ELSE $3 END,
			spent_period_start = $2
			WHERE id = $1 RETURNING spent`, e.KeyID, periodStart, e.Amount).Scan(&spent)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrKeyNotFound
	}
	if err != nil {
		return 0, false, err
	}
	return spent, duplicate, tx.Commit(ctx)
}
