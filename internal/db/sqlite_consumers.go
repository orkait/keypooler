package db

import (
	"context"
	"database/sql"
	"fmt"
)

// --- Consumers ---

func (a *SQLiteAdapter) CreateConsumer(ctx context.Context, consumer *Consumer) error {
	_, err := a.db.ExecContext(ctx,
		"INSERT INTO consumers (id, name, token_hash, description, is_active) VALUES (?, ?, ?, ?, ?)",
		consumer.ID, consumer.Name, consumer.TokenHash, consumer.Description, boolToInt(consumer.IsActive),
	)
	return err
}

const consumerColumns = "id, name, token_hash, description, is_active, created_at"

func scanConsumer(scan func(dest ...any) error) (*Consumer, error) {
	var c Consumer
	var isActive int
	if err := scan(&c.ID, &c.Name, &c.TokenHash, &c.Description, &isActive, &c.CreatedAt); err != nil {
		return nil, err
	}
	c.IsActive = isActive != 0
	return &c, nil
}

// GetConsumerByTokenHash returns the active consumer whose token_hash matches.
// Inactive consumers are excluded so a revoked token never authenticates.
func (a *SQLiteAdapter) GetConsumerByTokenHash(ctx context.Context, tokenHash string) (*Consumer, error) {
	row := a.db.QueryRowContext(ctx,
		"SELECT "+consumerColumns+" FROM consumers WHERE token_hash = ? AND is_active = 1",
		tokenHash,
	)
	c, err := scanConsumer(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (a *SQLiteAdapter) GetAllConsumers(ctx context.Context) ([]*Consumer, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT "+consumerColumns+" FROM consumers ORDER BY created_at",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var consumers []*Consumer
	for rows.Next() {
		c, err := scanConsumer(rows.Scan)
		if err != nil {
			return nil, err
		}
		consumers = append(consumers, c)
	}
	return consumers, rows.Err()
}

func (a *SQLiteAdapter) DeleteConsumer(ctx context.Context, id string) error {
	// Explicit child cleanup in a tx: FK enforcement is off by default on
	// SQLite/libSQL, so we must not rely on ON DELETE CASCADE - a delete must not
	// leave orphan scope rows that could re-grant access if an id were reused.
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM consumer_scopes WHERE consumer_id = ?", id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM consumers WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("consumer not found")
	}
	return tx.Commit()
}

// AddConsumerScope grants a consumer access to a tier. Idempotent: a duplicate
// (consumer_id, tier_id) is ignored rather than erroring.
func (a *SQLiteAdapter) AddConsumerScope(ctx context.Context, consumerID, tierID string) error {
	_, err := a.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO consumer_scopes (consumer_id, tier_id) VALUES (?, ?)",
		consumerID, tierID,
	)
	return err
}

func (a *SQLiteAdapter) GetConsumerScopes(ctx context.Context, consumerID string) ([]string, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT tier_id FROM consumer_scopes WHERE consumer_id = ?",
		consumerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tierIDs []string
	for rows.Next() {
		var tierID string
		if err := rows.Scan(&tierID); err != nil {
			return nil, err
		}
		tierIDs = append(tierIDs, tierID)
	}
	return tierIDs, rows.Err()
}

// --- Usage Events ---

func (a *SQLiteAdapter) RecordUsageEvent(ctx context.Context, keyID, consumerID, feature string) error {
	var consumer any
	if consumerID != "" {
		consumer = consumerID
	}
	_, err := a.db.ExecContext(ctx,
		"INSERT INTO usage_events (id, key_id, consumer_id, feature) VALUES (?, ?, ?, ?)",
		uuidString(), keyID, consumer, feature,
	)
	return err
}

func (a *SQLiteAdapter) ListUsageEvents(ctx context.Context, limit int) ([]*UsageEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := a.db.QueryContext(ctx,
		"SELECT id, key_id, consumer_id, feature, created_at FROM usage_events ORDER BY created_at DESC, id DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*UsageEvent
	for rows.Next() {
		var e UsageEvent
		var consumerID sql.NullString
		if err := rows.Scan(&e.ID, &e.KeyID, &consumerID, &e.Feature, &e.CreatedAt); err != nil {
			return nil, err
		}
		if consumerID.Valid {
			e.ConsumerID = consumerID.String
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}
