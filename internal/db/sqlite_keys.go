package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// --- Tiers ---

func (a *SQLiteAdapter) CreateTier(ctx context.Context, tier *Tier) error {
	_, err := a.db.ExecContext(ctx,
		"INSERT INTO tiers (id, name, description) VALUES (?, ?, ?)",
		tier.ID, tier.Name, tier.Description,
	)
	return err
}

func (a *SQLiteAdapter) GetTier(ctx context.Context, id string) (*Tier, error) {
	var t Tier
	err := a.db.QueryRowContext(ctx,
		"SELECT id, name, description, created_at FROM tiers WHERE id = ?", id,
	).Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tier not found")
	}
	return &t, err
}

func (a *SQLiteAdapter) GetTierByName(ctx context.Context, name string) (*Tier, error) {
	var t Tier
	err := a.db.QueryRowContext(ctx,
		"SELECT id, name, description, created_at FROM tiers WHERE name = ?", name,
	).Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &t, err
}

func (a *SQLiteAdapter) GetAllTiers(ctx context.Context) ([]*Tier, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT id, name, description, created_at FROM tiers ORDER BY name")
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

func (a *SQLiteAdapter) DeleteTier(ctx context.Context, id string) error {
	// Explicit child cleanup (FK enforcement is off by default): drop the tier's
	// features and any consumer scopes pointing at it so a reused tier id cannot
	// silently re-grant a previously-scoped consumer.
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM consumer_scopes WHERE tier_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM tier_features WHERE tier_id = ?", id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM tiers WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tier not found")
	}
	return tx.Commit()
}

func (a *SQLiteAdapter) UpdateTierDescription(ctx context.Context, id, description string) error {
	result, err := a.db.ExecContext(ctx,
		"UPDATE tiers SET description = ? WHERE id = ?",
		description, id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tier not found")
	}
	return nil
}

// --- Tier Features ---

func (a *SQLiteAdapter) SetTierFeatures(ctx context.Context, tierID string, features []*TierFeature) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing features for this tier
	if _, err := tx.ExecContext(ctx, "DELETE FROM tier_features WHERE tier_id = ?", tierID); err != nil {
		return err
	}

	// Insert new features
	for _, f := range features {
		window := f.WindowSeconds
		if window <= 0 {
			window = 60
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO tier_features (tier_id, feature, rate_limit, window_seconds) VALUES (?, ?, ?, ?)",
			tierID, f.Feature, f.RateLimit, window,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (a *SQLiteAdapter) GetTierFeatures(ctx context.Context, tierID string) ([]*TierFeature, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT tier_id, feature, rate_limit, window_seconds FROM tier_features WHERE tier_id = ? ORDER BY feature",
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

const keyColumns = "id, name, key_encrypted, tier_id, is_active, expires_at, usage_limit, usage_count, usage_window_seconds, usage_window_start, metadata_json, created_at"

// scanKey reads one key row in keyColumns order, parsing nullable and JSON fields.
func scanKey(scan func(dest ...any) error) (*Key, error) {
	var k Key
	var isActive int
	var expiresAt sql.NullTime
	var usageLimit sql.NullInt64
	var usageWindowSeconds sql.NullInt64
	var usageWindowStart sql.NullTime
	var metadataJSON string
	if err := scan(&k.ID, &k.Name, &k.KeyEncrypted, &k.TierID, &isActive, &expiresAt, &usageLimit, &k.UsageCount, &usageWindowSeconds, &usageWindowStart, &metadataJSON, &k.CreatedAt); err != nil {
		return nil, err
	}
	k.IsActive = isActive != 0
	if expiresAt.Valid {
		t := expiresAt.Time
		k.ExpiresAt = &t
	}
	if usageLimit.Valid {
		v := int(usageLimit.Int64)
		k.UsageLimit = &v
	}
	if usageWindowSeconds.Valid {
		v := int(usageWindowSeconds.Int64)
		k.UsageWindowSeconds = &v
	}
	if usageWindowStart.Valid {
		t := usageWindowStart.Time
		k.UsageWindowStart = &t
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

func (a *SQLiteAdapter) CreateKey(ctx context.Context, key *Key) error {
	metadataJSON, err := marshalMetadata(key.Metadata)
	if err != nil {
		return err
	}

	var expiresAt any
	if key.ExpiresAt != nil {
		expiresAt = *key.ExpiresAt
	}
	var usageLimit any
	if key.UsageLimit != nil {
		usageLimit = *key.UsageLimit
	}
	var usageWindowSeconds any
	if key.UsageWindowSeconds != nil {
		usageWindowSeconds = *key.UsageWindowSeconds
	}
	var usageWindowStart any
	if key.UsageWindowStart != nil {
		usageWindowStart = *key.UsageWindowStart
	}

	_, err = a.db.ExecContext(ctx,
		"INSERT INTO keys (id, name, key_encrypted, tier_id, is_active, expires_at, usage_limit, usage_count, usage_window_seconds, usage_window_start, metadata_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		key.ID, key.Name, key.KeyEncrypted, key.TierID, boolToInt(key.IsActive), expiresAt, usageLimit, key.UsageCount, usageWindowSeconds, usageWindowStart, metadataJSON,
	)
	return err
}

func (a *SQLiteAdapter) GetKey(ctx context.Context, id string) (*Key, error) {
	row := a.db.QueryRowContext(ctx,
		"SELECT "+keyColumns+" FROM keys WHERE id = ?", id,
	)
	k, err := scanKey(row.Scan)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("key not found")
	}
	return k, err
}

func (a *SQLiteAdapter) GetAllKeys(ctx context.Context) ([]*Key, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT "+keyColumns+" FROM keys ORDER BY created_at",
	)
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

func (a *SQLiteAdapter) GetKeysByTier(ctx context.Context, tierID string) ([]*Key, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT "+keyColumns+" FROM keys WHERE tier_id = ? ORDER BY created_at",
		tierID,
	)
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

func (a *SQLiteAdapter) DeleteKey(ctx context.Context, id string) error {
	// Explicit child cleanup (FK enforcement is off by default): drop the key's
	// bound secrets so deleting a key cannot leave orphan encrypted secrets behind.
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM key_secrets WHERE key_id = ?", id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM keys WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found")
	}
	return tx.Commit()
}

func (a *SQLiteAdapter) SetKeyActive(ctx context.Context, id string, active bool) error {
	result, err := a.db.ExecContext(ctx,
		"UPDATE keys SET is_active = ? WHERE id = ?",
		boolToInt(active), id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found")
	}
	return nil
}

func (a *SQLiteAdapter) IncrementUsage(ctx context.Context, keyID string) error {
	result, err := a.db.ExecContext(ctx,
		"UPDATE keys SET usage_count = usage_count + 1 WHERE id = ?",
		keyID,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found")
	}
	return nil
}

// ResetUsageWindow zeroes the cumulative usage count and stamps a new window
// start. Called when a windowed (e.g. monthly) usage budget has rolled over.
// usage_count is set to 1 to account for the serve that triggered the reset, so
// the DB stays consistent with the in-memory count (reset-to-0 then increment).
func (a *SQLiteAdapter) ResetUsageWindow(ctx context.Context, keyID string, start time.Time) error {
	result, err := a.db.ExecContext(ctx,
		"UPDATE keys SET usage_count = 1, usage_window_start = ? WHERE id = ?",
		start, keyID,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found")
	}
	return nil
}

// --- Key Secrets ---

func (a *SQLiteAdapter) GetKeySecrets(ctx context.Context, keyID string) ([]*KeySecret, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT key_id, name, value_encrypted FROM key_secrets WHERE key_id = ? ORDER BY name",
		keyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var secrets []*KeySecret
	for rows.Next() {
		var s KeySecret
		if err := rows.Scan(&s.KeyID, &s.Name, &s.ValueEncrypted); err != nil {
			return nil, err
		}
		secrets = append(secrets, &s)
	}
	return secrets, rows.Err()
}

// SetKeySecrets replaces all secrets for a key inside a single transaction.
func (a *SQLiteAdapter) SetKeySecrets(ctx context.Context, keyID string, secrets []*KeySecret) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM key_secrets WHERE key_id = ?", keyID); err != nil {
		return err
	}

	for _, s := range secrets {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO key_secrets (key_id, name, value_encrypted) VALUES (?, ?, ?)",
			keyID, s.Name, s.ValueEncrypted,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}
