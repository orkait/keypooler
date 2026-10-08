package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- Tiers ---

func (a *PostgresAdapter) CreateTier(ctx context.Context, tier *Tier) error {
	_, err := a.pool.Exec(ctx,
		"INSERT INTO tiers (id, name, description) VALUES ($1, $2, $3)",
		tier.ID, tier.Name, tier.Description,
	)
	return err
}

func (a *PostgresAdapter) GetTier(ctx context.Context, id string) (*Tier, error) {
	var t Tier
	err := a.pool.QueryRow(ctx,
		"SELECT id, name, description, created_at FROM tiers WHERE id = $1", id,
	).Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt)
	if err != nil {
		return nil, notFound(err, "tier")
	}
	return &t, nil
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

func (a *PostgresAdapter) DeleteTier(ctx context.Context, id string) error {
	// Explicit child cleanup in one transaction: a reused tier id must never
	// silently re-grant a previously-scoped consumer.
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "DELETE FROM consumer_scopes WHERE tier_id = $1", id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM tier_features WHERE tier_id = $1", id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM tiers WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tier not found")
	}
	return tx.Commit(ctx)
}

func (a *PostgresAdapter) UpdateTierDescription(ctx context.Context, id, description string) error {
	tag, err := a.pool.Exec(ctx,
		"UPDATE tiers SET description = $1 WHERE id = $2",
		description, id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tier not found")
	}
	return nil
}

// --- Tier Features ---

func (a *PostgresAdapter) SetTierFeatures(ctx context.Context, tierID string, features []*TierFeature) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM tier_features WHERE tier_id = $1", tierID); err != nil {
		return err
	}
	for _, f := range features {
		if _, err := tx.Exec(ctx,
			"INSERT INTO tier_features (tier_id, feature, rate_limit, window_seconds) VALUES ($1, $2, $3, $4)",
			tierID, f.Feature, f.RateLimit, f.WindowSeconds,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (a *PostgresAdapter) GetTierFeatures(ctx context.Context, tierID string) ([]*TierFeature, error) {
	rows, err := a.pool.Query(ctx,
		"SELECT tier_id, feature, rate_limit, window_seconds FROM tier_features WHERE tier_id = $1 ORDER BY feature",
		tierID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var features []*TierFeature
	for rows.Next() {
		var f TierFeature
		if err := rows.Scan(&f.TierID, &f.Feature, &f.RateLimit, &f.WindowSeconds); err != nil {
			return nil, err
		}
		features = append(features, &f)
	}
	return features, rows.Err()
}

// --- Keys ---

const keyColumns = "id, name, key_value, tier_id, is_active, expires_at, usage_limit, usage_count, usage_window_seconds, usage_window_start, exhausted_until, metadata_json, created_at"

// scanKey reads one key row in keyColumns order; NULLs land as nil pointers.
func scanKey(scan func(dest ...any) error) (*Key, error) {
	var k Key
	var metadataJSON string
	if err := scan(&k.ID, &k.Name, &k.KeyValue, &k.TierID, &k.IsActive, &k.ExpiresAt, &k.UsageLimit, &k.UsageCount, &k.UsageWindowSeconds, &k.UsageWindowStart, &k.ExhaustedUntil, &metadataJSON, &k.CreatedAt); err != nil {
		return nil, err
	}
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	if err := json.Unmarshal([]byte(metadataJSON), &k.Metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata_json for key %s: %w", k.ID, err)
	}
	if k.Metadata == nil {
		k.Metadata = map[string]any{}
	}
	return &k, nil
}

func (a *PostgresAdapter) CreateKey(ctx context.Context, key *Key) error {
	metadataJSON, err := marshalMetadata(key.Metadata)
	if err != nil {
		return err
	}
	_, err = a.pool.Exec(ctx,
		"INSERT INTO keys (id, name, key_value, tier_id, is_active, expires_at, usage_limit, usage_count, usage_window_seconds, usage_window_start, metadata_json) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)",
		key.ID, key.Name, key.KeyValue, key.TierID, key.IsActive, key.ExpiresAt, key.UsageLimit, key.UsageCount, key.UsageWindowSeconds, key.UsageWindowStart, metadataJSON,
	)
	return err
}

func (a *PostgresAdapter) GetKey(ctx context.Context, id string) (*Key, error) {
	k, err := scanKey(a.pool.QueryRow(ctx, "SELECT "+keyColumns+" FROM keys WHERE id = $1", id).Scan)
	if err != nil {
		return nil, notFound(err, "key")
	}
	return k, nil
}

func (a *PostgresAdapter) GetAllKeys(ctx context.Context) ([]*Key, error) {
	return a.queryKeys(ctx, "SELECT "+keyColumns+" FROM keys ORDER BY created_at")
}

func (a *PostgresAdapter) GetKeysByTier(ctx context.Context, tierID string) ([]*Key, error) {
	return a.queryKeys(ctx, "SELECT "+keyColumns+" FROM keys WHERE tier_id = $1 ORDER BY created_at", tierID)
}

func (a *PostgresAdapter) queryKeys(ctx context.Context, sql string, args ...any) ([]*Key, error) {
	rows, err := a.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*Key
	for rows.Next() {
		k, err := scanKey(rows.Scan)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func marshalMetadata(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}
	return string(b), nil
}

func (a *PostgresAdapter) DeleteKey(ctx context.Context, id string) error {
	// Explicit child cleanup in one transaction: deleting a key never leaves its
	// bound secrets behind.
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

// updateKey runs a single-row UPDATE on keys and reports a missing key.
func (a *PostgresAdapter) updateKey(ctx context.Context, sql string, args ...any) error {
	tag, err := a.pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("key not found")
	}
	return nil
}

func (a *PostgresAdapter) SetKeyActive(ctx context.Context, id string, active bool) error {
	return a.updateKey(ctx, "UPDATE keys SET is_active = $1 WHERE id = $2", active, id)
}

// AddUsage adds n serves to a key's cumulative usage count.
func (a *PostgresAdapter) AddUsage(ctx context.Context, keyID string, n int) error {
	return a.updateKey(ctx, "UPDATE keys SET usage_count = usage_count + $1 WHERE id = $2", n, keyID)
}

// ResetUsageWindow sets the usage count and stamps a new window start, for a
// windowed (e.g. monthly) budget that rolled over. count is the serves made since
// the reset, including the one that triggered it.
func (a *PostgresAdapter) ResetUsageWindow(ctx context.Context, keyID string, start time.Time, count int) error {
	return a.updateKey(ctx, "UPDATE keys SET usage_count = $1, usage_window_start = $2 WHERE id = $3", count, start, keyID)
}

func (a *PostgresAdapter) SetKeyExhausted(ctx context.Context, keyID string, until time.Time) error {
	return a.updateKey(ctx, "UPDATE keys SET exhausted_until = $1 WHERE id = $2", until, keyID)
}

// --- Key Secrets ---

func (a *PostgresAdapter) GetKeySecrets(ctx context.Context, keyID string) ([]*KeySecret, error) {
	rows, err := a.pool.Query(ctx,
		"SELECT key_id, name, value FROM key_secrets WHERE key_id = $1 ORDER BY name",
		keyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var secrets []*KeySecret
	for rows.Next() {
		var s KeySecret
		if err := rows.Scan(&s.KeyID, &s.Name, &s.Value); err != nil {
			return nil, err
		}
		secrets = append(secrets, &s)
	}
	return secrets, rows.Err()
}

// SetKeySecrets replaces all secrets for a key inside a single transaction.
func (a *PostgresAdapter) SetKeySecrets(ctx context.Context, keyID string, secrets []*KeySecret) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM key_secrets WHERE key_id = $1", keyID); err != nil {
		return err
	}
	for _, s := range secrets {
		if _, err := tx.Exec(ctx,
			"INSERT INTO key_secrets (key_id, name, value) VALUES ($1, $2, $3)",
			keyID, s.Name, s.Value,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
