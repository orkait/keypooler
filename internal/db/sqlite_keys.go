package db

import (
	"context"
	"database/sql"
	"fmt"
)

// --- Tiers ---

func (a *SQLiteAdapter) CreateTier(ctx context.Context, tier *Tier) error {
	_, err := a.db.ExecContext(ctx,
		"INSERT INTO tiers (id, name) VALUES (?, ?)",
		tier.ID, tier.Name,
	)
	return err
}

func (a *SQLiteAdapter) GetTier(ctx context.Context, id string) (*Tier, error) {
	var t Tier
	err := a.db.QueryRowContext(ctx,
		"SELECT id, name, created_at FROM tiers WHERE id = ?", id,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tier not found")
	}
	return &t, err
}

func (a *SQLiteAdapter) GetTierByName(ctx context.Context, name string) (*Tier, error) {
	var t Tier
	err := a.db.QueryRowContext(ctx,
		"SELECT id, name, created_at FROM tiers WHERE name = ?", name,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &t, err
}

func (a *SQLiteAdapter) GetAllTiers(ctx context.Context) ([]*Tier, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT id, name, created_at FROM tiers ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tiers []*Tier
	for rows.Next() {
		var t Tier
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		tiers = append(tiers, &t)
	}
	return tiers, rows.Err()
}

func (a *SQLiteAdapter) DeleteTier(ctx context.Context, id string) error {
	result, err := a.db.ExecContext(ctx, "DELETE FROM tiers WHERE id = ?", id)
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
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO tier_features (tier_id, feature, rate_per_minute) VALUES (?, ?, ?)",
			tierID, f.Feature, f.RatePerMinute,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (a *SQLiteAdapter) GetTierFeatures(ctx context.Context, tierID string) ([]*TierFeature, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT tier_id, feature, rate_per_minute FROM tier_features WHERE tier_id = ? ORDER BY feature",
		tierID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var features []*TierFeature
	for rows.Next() {
		var f TierFeature
		if err := rows.Scan(&f.TierID, &f.Feature, &f.RatePerMinute); err != nil {
			return nil, err
		}
		features = append(features, &f)
	}
	return features, rows.Err()
}

// --- Keys ---

func (a *SQLiteAdapter) CreateKey(ctx context.Context, key *Key) error {
	_, err := a.db.ExecContext(ctx,
		"INSERT INTO keys (id, name, key_encrypted, tier_id, is_active) VALUES (?, ?, ?, ?, ?)",
		key.ID, key.Name, key.KeyEncrypted, key.TierID, boolToInt(key.IsActive),
	)
	return err
}

func (a *SQLiteAdapter) GetKey(ctx context.Context, id string) (*Key, error) {
	var k Key
	var isActive int
	err := a.db.QueryRowContext(ctx,
		"SELECT id, name, key_encrypted, tier_id, is_active, created_at FROM keys WHERE id = ?", id,
	).Scan(&k.ID, &k.Name, &k.KeyEncrypted, &k.TierID, &isActive, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("key not found")
	}
	k.IsActive = isActive != 0
	return &k, err
}

func (a *SQLiteAdapter) GetAllKeys(ctx context.Context) ([]*Key, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT id, name, key_encrypted, tier_id, is_active, created_at FROM keys ORDER BY created_at",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*Key
	for rows.Next() {
		var k Key
		var isActive int
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyEncrypted, &k.TierID, &isActive, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.IsActive = isActive != 0
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

func (a *SQLiteAdapter) GetKeysByTier(ctx context.Context, tierID string) ([]*Key, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT id, name, key_encrypted, tier_id, is_active, created_at FROM keys WHERE tier_id = ? ORDER BY created_at",
		tierID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*Key
	for rows.Next() {
		var k Key
		var isActive int
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyEncrypted, &k.TierID, &isActive, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.IsActive = isActive != 0
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

func (a *SQLiteAdapter) DeleteKey(ctx context.Context, id string) error {
	result, err := a.db.ExecContext(ctx, "DELETE FROM keys WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found")
	}
	return nil
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
