package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (a *PostgresAdapter) CreateTier(ctx context.Context, tier *Tier, features []*TierFeature) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		"INSERT INTO tiers (id, name, description) VALUES ($1, $2, $3)",
		tier.ID, tier.Name, tier.Description,
	); err != nil {
		return duplicate(err)
	}
	if err := insertTierFeatures(ctx, tx, tier.ID, features); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *PostgresAdapter) GetTierByName(ctx context.Context, name string) (*Tier, error) {
	var t Tier
	err := a.pool.QueryRow(ctx,
		"SELECT id, name, description, created_at FROM tiers WHERE name = $1", name,
	).Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (a *PostgresAdapter) GetAllTiers(ctx context.Context) ([]*Tier, error) {
	rows, err := a.pool.Query(ctx, "SELECT id, name, description, created_at FROM tiers ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tiers []*Tier
	for rows.Next() {
		var t Tier
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt); err != nil {
			return nil, err
		}
		tiers = append(tiers, &t)
	}
	return tiers, rows.Err()
}

func (a *PostgresAdapter) UpdateTierDescription(ctx context.Context, id, description string) error {
	_, err := a.pool.Exec(ctx, "UPDATE tiers SET description = $1 WHERE id = $2", description, id)
	return err
}

func (a *PostgresAdapter) SetTierFeatures(ctx context.Context, tierID string, features []*TierFeature) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM tier_features WHERE tier_id = $1", tierID); err != nil {
		return err
	}
	if err := insertTierFeatures(ctx, tx, tierID, features); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertTierFeatures(ctx context.Context, tx pgx.Tx, tierID string, features []*TierFeature) error {
	for _, f := range features {
		if _, err := tx.Exec(ctx,
			"INSERT INTO tier_features (tier_id, feature, rate_limit, window_seconds) VALUES ($1, $2, $3, $4)",
			tierID, f.Feature, f.RateLimit, f.WindowSeconds,
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *PostgresAdapter) TierFeaturesByTier(ctx context.Context) (map[string][]*TierFeature, error) {
	rows, err := a.pool.Query(ctx,
		"SELECT tier_id, feature, rate_limit, window_seconds FROM tier_features ORDER BY tier_id, feature",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byTier := map[string][]*TierFeature{}
	for rows.Next() {
		var f TierFeature
		if err := rows.Scan(&f.TierID, &f.Feature, &f.RateLimit, &f.WindowSeconds); err != nil {
			return nil, err
		}
		byTier[f.TierID] = append(byTier[f.TierID], &f)
	}
	return byTier, rows.Err()
}

func (a *PostgresAdapter) CreateKey(ctx context.Context, key *Key, secrets []*KeySecret) error {
	metadata, err := json.Marshal(key.Metadata)
	if err != nil {
		return err
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	budgetAmount, budgetUnit, budgetResetDay := budgetColumns(key.Budget)
	if _, err := tx.Exec(ctx,
		"INSERT INTO keys (id, name, key_value, tier_id, is_active, expires_at, usage_limit, usage_count, usage_window_seconds, usage_window_start, metadata_json, budget_amount, budget_unit, budget_reset_day) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)",
		key.ID, key.Name, key.KeyValue, key.TierID, key.IsActive, key.ExpiresAt, key.UsageLimit, key.UsageCount, key.UsageWindowSeconds, key.UsageWindowStart, string(metadata), budgetAmount, budgetUnit, budgetResetDay,
	); err != nil {
		return err
	}
	for _, s := range secrets {
		if _, err := tx.Exec(ctx,
			"INSERT INTO key_secrets (key_id, name, value) VALUES ($1, $2, $3)",
			key.ID, s.Name, s.Value,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (a *PostgresAdapter) GetAllKeys(ctx context.Context) ([]*Key, error) {
	rows, err := a.pool.Query(ctx, "SELECT id, name, key_value, tier_id, is_active, expires_at, usage_limit, usage_count, usage_window_seconds, usage_window_start, exhausted_until, metadata_json, created_at, budget_amount, budget_unit, budget_reset_day, spent, spent_period_start FROM keys ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*Key
	for rows.Next() {
		var k Key
		var metadata string
		var budgetAmount *float64
		var budgetUnit *string
		var budgetResetDay *int
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyValue, &k.TierID, &k.IsActive, &k.ExpiresAt, &k.UsageLimit, &k.UsageCount, &k.UsageWindowSeconds, &k.UsageWindowStart, &k.ExhaustedUntil, &metadata, &k.CreatedAt,
			&budgetAmount, &budgetUnit, &budgetResetDay, &k.Spent, &k.SpentPeriodStart); err != nil {
			return nil, err
		}
		k.Budget = budgetOf(budgetAmount, budgetUnit, budgetResetDay)
		if metadata == "" {
			metadata = "{}"
		}
		if err := json.Unmarshal([]byte(metadata), &k.Metadata); err != nil {
			return nil, fmt.Errorf("failed to parse metadata_json for key %s: %w", k.ID, err)
		}
		if k.Metadata == nil {
			k.Metadata = map[string]any{}
		}
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

func (a *PostgresAdapter) DeleteKey(ctx context.Context, id string) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "DELETE FROM key_secrets WHERE key_id = $1", id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM keys WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("key not found")
	}
	return tx.Commit(ctx)
}

func (a *PostgresAdapter) AddUsage(ctx context.Context, keyID string, n int) error {
	_, err := a.pool.Exec(ctx, "UPDATE keys SET usage_count = usage_count + $1 WHERE id = $2", n, keyID)
	return err
}

func (a *PostgresAdapter) ResetUsageWindow(ctx context.Context, keyID string, start time.Time, count int) error {
	_, err := a.pool.Exec(ctx, "UPDATE keys SET usage_count = $1, usage_window_start = $2 WHERE id = $3", count, start, keyID)
	return err
}

func (a *PostgresAdapter) SetKeyExhausted(ctx context.Context, keyID string, until time.Time) error {
	_, err := a.pool.Exec(ctx, "UPDATE keys SET exhausted_until = $1 WHERE id = $2", until, keyID)
	return err
}

func (a *PostgresAdapter) KeySecretsByKey(ctx context.Context) (map[string][]*KeySecret, error) {
	rows, err := a.pool.Query(ctx, "SELECT key_id, name, value FROM key_secrets ORDER BY key_id, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byKey := map[string][]*KeySecret{}
	for rows.Next() {
		var s KeySecret
		if err := rows.Scan(&s.KeyID, &s.Name, &s.Value); err != nil {
			return nil, err
		}
		byKey[s.KeyID] = append(byKey[s.KeyID], &s)
	}
	return byKey, rows.Err()
}
