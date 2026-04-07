package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (a *SQLiteAdapter) CreateExecution(ctx context.Context, exec *Execution) error {
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO executions (id, script, function_name, version_id, status, trigger_type, callback_url, input, attempts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exec.ID, exec.Script, exec.FunctionName, exec.VersionID, exec.Status, exec.TriggerType,
		exec.CallbackURL, exec.Input, exec.Attempts,
	)
	return err
}

func (a *SQLiteAdapter) GetExecution(ctx context.Context, id string) (*Execution, error) {
	var e Execution
	var versionID, keyID, callbackURL, input, output, errStr sql.NullString
	var completedAt sql.NullTime

	err := a.db.QueryRowContext(ctx,
		`SELECT id, script, function_name, version_id, key_id, status, trigger_type, callback_url,
		        input, output, error, attempts, created_at, completed_at
		 FROM executions WHERE id = ?`, id,
	).Scan(&e.ID, &e.Script, &e.FunctionName, &versionID, &keyID, &e.Status, &e.TriggerType,
		&callbackURL, &input, &output, &errStr, &e.Attempts, &e.CreatedAt, &completedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if versionID.Valid {
		e.VersionID = &versionID.String
	}
	if keyID.Valid {
		e.KeyID = &keyID.String
	}
	if callbackURL.Valid {
		e.CallbackURL = &callbackURL.String
	}
	if input.Valid {
		e.Input = &input.String
	}
	if output.Valid {
		e.Output = &output.String
	}
	if errStr.Valid {
		e.Error = &errStr.String
	}
	if completedAt.Valid {
		e.CompletedAt = &completedAt.Time
	}

	return &e, nil
}

func (a *SQLiteAdapter) UpdateExecutionStatus(ctx context.Context, id, status, keyID string, attempts int) error {
	var keyIDVal any
	if keyID != "" {
		keyIDVal = keyID
	}
	result, err := a.db.ExecContext(ctx,
		"UPDATE executions SET status = ?, key_id = ?, attempts = ? WHERE id = ?",
		status, keyIDVal, attempts, id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("execution not found")
	}
	return nil
}

func (a *SQLiteAdapter) UpdateExecutionResult(ctx context.Context, id, status, output, errMsg string, completedAt time.Time) error {
	var outputVal, errVal any
	if output != "" {
		outputVal = output
	}
	if errMsg != "" {
		errVal = errMsg
	}
	result, err := a.db.ExecContext(ctx,
		"UPDATE executions SET status = ?, output = ?, error = ?, completed_at = ? WHERE id = ?",
		status, outputVal, errVal, completedAt, id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("execution not found")
	}
	return nil
}

func (a *SQLiteAdapter) GetExecutionsByStatus(ctx context.Context, status string, limit int) ([]*Execution, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, script, function_name, version_id, key_id, status, trigger_type, callback_url,
		        input, output, error, attempts, created_at, completed_at
		 FROM executions WHERE status = ? ORDER BY created_at ASC LIMIT ?`,
		status, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*Execution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func scanExecution(rows *sql.Rows) (*Execution, error) {
	var e Execution
	var versionID, keyID, callbackURL, input, output, errStr sql.NullString
	var completedAt sql.NullTime

	err := rows.Scan(&e.ID, &e.Script, &e.FunctionName, &versionID, &keyID, &e.Status, &e.TriggerType,
		&callbackURL, &input, &output, &errStr, &e.Attempts, &e.CreatedAt, &completedAt)
	if err != nil {
		return nil, err
	}

	if versionID.Valid {
		e.VersionID = &versionID.String
	}
	if keyID.Valid {
		e.KeyID = &keyID.String
	}
	if callbackURL.Valid {
		e.CallbackURL = &callbackURL.String
	}
	if input.Valid {
		e.Input = &input.String
	}
	if output.Valid {
		e.Output = &output.String
	}
	if errStr.Valid {
		e.Error = &errStr.String
	}
	if completedAt.Valid {
		e.CompletedAt = &completedAt.Time
	}

	return &e, nil
}

// --- Dead Letter ---

func (a *SQLiteAdapter) CreateDeadLetter(ctx context.Context, dl *DeadLetter) error {
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO dead_letter (id, execution_id, script, function_name, version_id, input, error, attempts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		dl.ID, dl.ExecutionID, dl.Script, dl.FunctionName, dl.VersionID, dl.Input, dl.Error, dl.Attempts,
	)
	return err
}

func (a *SQLiteAdapter) GetDeadLetters(ctx context.Context, limit int) ([]*DeadLetter, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, execution_id, script, function_name, version_id, input, error, attempts, failed_at
		 FROM dead_letter ORDER BY failed_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dls []*DeadLetter
	for rows.Next() {
		var dl DeadLetter
		var versionID, input, errStr sql.NullString
		if err := rows.Scan(&dl.ID, &dl.ExecutionID, &dl.Script, &dl.FunctionName, &versionID,
			&input, &errStr, &dl.Attempts, &dl.FailedAt); err != nil {
			return nil, err
		}
		if versionID.Valid {
			dl.VersionID = &versionID.String
		}
		if input.Valid {
			dl.Input = &input.String
		}
		if errStr.Valid {
			dl.Error = &errStr.String
		}
		dls = append(dls, &dl)
	}
	return dls, rows.Err()
}

func (a *SQLiteAdapter) GetDeadLetter(ctx context.Context, id string) (*DeadLetter, error) {
	var dl DeadLetter
	var versionID, input, errStr sql.NullString
	err := a.db.QueryRowContext(ctx,
		`SELECT id, execution_id, script, function_name, version_id, input, error, attempts, failed_at
		 FROM dead_letter WHERE id = ?`, id,
	).Scan(&dl.ID, &dl.ExecutionID, &dl.Script, &dl.FunctionName, &versionID,
		&input, &errStr, &dl.Attempts, &dl.FailedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if versionID.Valid {
		dl.VersionID = &versionID.String
	}
	if input.Valid {
		dl.Input = &input.String
	}
	if errStr.Valid {
		dl.Error = &errStr.String
	}
	return &dl, nil
}

func (a *SQLiteAdapter) DeleteDeadLetter(ctx context.Context, id string) error {
	result, err := a.db.ExecContext(ctx, "DELETE FROM dead_letter WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("dead letter entry not found")
	}
	return nil
}
