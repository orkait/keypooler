package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const consumerColumns = "id, name, token_hash, description, is_active, created_at"

func (a *PostgresAdapter) CreateConsumer(ctx context.Context, consumer *Consumer) error {
	_, err := a.pool.Exec(ctx,
		"INSERT INTO consumers (id, name, token_hash, description, is_active) VALUES ($1, $2, $3, $4, $5)",
		consumer.ID, consumer.Name, consumer.TokenHash, consumer.Description, consumer.IsActive,
	)
	return duplicate(err)
}

func scanConsumer(scan func(dest ...any) error) (*Consumer, error) {
	var c Consumer
	if err := scan(&c.ID, &c.Name, &c.TokenHash, &c.Description, &c.IsActive, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (a *PostgresAdapter) GetConsumerByTokenHash(ctx context.Context, tokenHash string) (*Consumer, error) {
	c, err := scanConsumer(a.pool.QueryRow(ctx,
		"SELECT "+consumerColumns+" FROM consumers WHERE token_hash = $1 AND is_active",
		tokenHash,
	).Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (a *PostgresAdapter) GetAllConsumers(ctx context.Context) ([]*Consumer, error) {
	rows, err := a.pool.Query(ctx, "SELECT "+consumerColumns+" FROM consumers ORDER BY created_at")
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

func (a *PostgresAdapter) DeleteConsumer(ctx context.Context, id string) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "DELETE FROM consumer_scopes WHERE consumer_id = $1", id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM consumers WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("consumer not found")
	}
	return tx.Commit(ctx)
}

func (a *PostgresAdapter) AddConsumerScope(ctx context.Context, consumerID, tierID string) error {
	_, err := a.pool.Exec(ctx,
		"INSERT INTO consumer_scopes (consumer_id, tier_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		consumerID, tierID,
	)
	return err
}

func (a *PostgresAdapter) GetConsumerScopes(ctx context.Context, consumerID string) ([]string, error) {
	rows, err := a.pool.Query(ctx, "SELECT tier_id FROM consumer_scopes WHERE consumer_id = $1", consumerID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (a *PostgresAdapter) ConsumerScopesByConsumer(ctx context.Context) (map[string][]string, error) {
	rows, err := a.pool.Query(ctx, "SELECT consumer_id, tier_id FROM consumer_scopes ORDER BY consumer_id, tier_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byConsumer := map[string][]string{}
	for rows.Next() {
		var consumerID, tierID string
		if err := rows.Scan(&consumerID, &tierID); err != nil {
			return nil, err
		}
		byConsumer[consumerID] = append(byConsumer[consumerID], tierID)
	}
	return byConsumer, rows.Err()
}

func (a *PostgresAdapter) RecordUsageEvents(ctx context.Context, events []*UsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	_, err := a.pool.CopyFrom(ctx,
		pgx.Identifier{"usage_events"},
		[]string{"id", "key_id", "consumer_id", "feature", "created_at"},
		pgx.CopyFromSlice(len(events), func(i int) ([]any, error) {
			e := events[i]
			var consumer *string
			if e.ConsumerID != "" {
				consumer = &e.ConsumerID
			}
			return []any{e.ID, e.KeyID, consumer, e.Feature, e.CreatedAt}, nil
		}),
	)
	return err
}

func (a *PostgresAdapter) ListUsageEvents(ctx context.Context, limit int) ([]*UsageEvent, error) {
	rows, err := a.pool.Query(ctx,
		"SELECT id, key_id, consumer_id, feature, created_at FROM usage_events ORDER BY created_at DESC, id DESC LIMIT $1",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*UsageEvent
	for rows.Next() {
		var e UsageEvent
		var consumerID *string
		if err := rows.Scan(&e.ID, &e.KeyID, &consumerID, &e.Feature, &e.CreatedAt); err != nil {
			return nil, err
		}
		if consumerID != nil {
			e.ConsumerID = *consumerID
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}

func NewUsageEvent(keyID, consumerID, feature string) *UsageEvent {
	return &UsageEvent{ID: uuid.New().String(), KeyID: keyID, ConsumerID: consumerID, Feature: feature, CreatedAt: time.Now().UTC()}
}
