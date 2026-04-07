package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (a *SQLiteAdapter) CreateIntegrationVersion(ctx context.Context, version *IntegrationVersion) (*IntegrationVersion, error) {
	if version == nil {
		return nil, fmt.Errorf("integration version is required")
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var nextVersion int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1
		 FROM integration_versions
		 WHERE integration_name = ? AND function_name = ?`,
		version.IntegrationName, version.FunctionName,
	).Scan(&nextVersion); err != nil {
		return nil, err
	}

	status := version.Status
	if status == "" {
		status = IntegrationVersionStatusDraft
	}

	id := version.ID
	if id == "" {
		id = uuid.New().String()
	}

	sum := sha256.Sum256([]byte(version.Code))
	checksum := version.Checksum
	if checksum == "" {
		checksum = hex.EncodeToString(sum[:])
	}

	created := version.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO integration_versions (
			id, integration_name, function_name, version, runtime, feature,
			contract_json, code, status, checksum, created_by, created_at, activated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		id, version.IntegrationName, version.FunctionName, nextVersion,
		version.Runtime, version.Feature, version.ContractJSON, version.Code,
		status, checksum, version.CreatedBy, created,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	createdVersion := *version
	createdVersion.ID = id
	createdVersion.Version = nextVersion
	createdVersion.Status = status
	createdVersion.Checksum = checksum
	createdVersion.CreatedAt = created
	createdVersion.ActivatedAt = nil
	return &createdVersion, nil
}

func (a *SQLiteAdapter) GetIntegrationVersion(ctx context.Context, id string) (*IntegrationVersion, error) {
	row := a.db.QueryRowContext(ctx,
		`SELECT id, integration_name, function_name, version, runtime, feature,
		        contract_json, code, status, checksum, created_by, created_at, activated_at
		 FROM integration_versions WHERE id = ?`,
		id,
	)
	return scanIntegrationVersion(row)
}

func (a *SQLiteAdapter) GetActiveIntegrationVersion(ctx context.Context, integrationName, functionName string) (*IntegrationVersion, error) {
	row := a.db.QueryRowContext(ctx,
		`SELECT id, integration_name, function_name, version, runtime, feature,
		        contract_json, code, status, checksum, created_by, created_at, activated_at
		 FROM integration_versions
		 WHERE integration_name = ? AND function_name = ? AND status = ?
		 ORDER BY version DESC
		 LIMIT 1`,
		integrationName, functionName, IntegrationVersionStatusActive,
	)
	return scanIntegrationVersion(row)
}

func (a *SQLiteAdapter) ListIntegrationVersions(ctx context.Context, integrationName, functionName string) ([]*IntegrationVersion, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, integration_name, function_name, version, runtime, feature,
		        contract_json, code, status, checksum, created_by, created_at, activated_at
		 FROM integration_versions
		 WHERE integration_name = ? AND function_name = ?
		 ORDER BY version DESC`,
		integrationName, functionName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []*IntegrationVersion
	for rows.Next() {
		v, err := scanIntegrationVersionFromRows(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (a *SQLiteAdapter) ListActiveIntegrationVersions(ctx context.Context) ([]*IntegrationVersion, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, integration_name, function_name, version, runtime, feature,
		        contract_json, code, status, checksum, created_by, created_at, activated_at
		 FROM integration_versions
		 WHERE status = ?
		 ORDER BY integration_name, function_name`,
		IntegrationVersionStatusActive,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []*IntegrationVersion
	for rows.Next() {
		v, err := scanIntegrationVersionFromRows(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (a *SQLiteAdapter) ActivateIntegrationVersion(ctx context.Context, id string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var integrationName, functionName string
	err = tx.QueryRowContext(ctx,
		`SELECT integration_name, function_name
		 FROM integration_versions
		 WHERE id = ?`,
		id,
	).Scan(&integrationName, &functionName)
	if err == sql.ErrNoRows {
		return fmt.Errorf("integration version not found")
	}
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE integration_versions
		 SET status = ?, activated_at = NULL
		 WHERE integration_name = ? AND function_name = ? AND status = ?`,
		IntegrationVersionStatusDisabled, integrationName, functionName, IntegrationVersionStatusActive,
	); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE integration_versions
		 SET status = ?, activated_at = ?
		 WHERE id = ?`,
		IntegrationVersionStatusActive, time.Now().UTC(), id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("integration version not found")
	}

	return tx.Commit()
}

type integrationVersionScanner interface {
	Scan(dest ...any) error
}

func scanIntegrationVersion(scanner integrationVersionScanner) (*IntegrationVersion, error) {
	var activatedAt sql.NullTime
	var version IntegrationVersion
	err := scanner.Scan(
		&version.ID,
		&version.IntegrationName,
		&version.FunctionName,
		&version.Version,
		&version.Runtime,
		&version.Feature,
		&version.ContractJSON,
		&version.Code,
		&version.Status,
		&version.Checksum,
		&version.CreatedBy,
		&version.CreatedAt,
		&activatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if activatedAt.Valid {
		version.ActivatedAt = &activatedAt.Time
	}
	return &version, nil
}

func scanIntegrationVersionFromRows(rows *sql.Rows) (*IntegrationVersion, error) {
	return scanIntegrationVersion(rows)
}
