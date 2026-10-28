package sqlite

import (
	"context"
	"fmt"
	"time"
)

// Settings returns every runtime setting stored in the database. The map only
// contains keys that were saved; callers merge the defaults themselves.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT key, value FROM app_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, rows.Err()
}

// SeedSettings inserts keys that are not stored yet, keeping existing rows.
// On later starts, values edited in the console remain the source of truth.
func (s *Store) SeedSettings(ctx context.Context, values map[string]string, now time.Time) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		"INSERT OR IGNORE INTO app_settings(key, value, updated_at) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for key, value := range values {
		if _, err := stmt.ExecContext(ctx, key, value, millis(now)); err != nil {
			return fmt.Errorf("seed setting %s: %w", key, err)
		}
	}
	return tx.Commit()
}

// UpdateSettings upserts the given keys. Values are stored as provided; secret
// encryption happens in the application layer.
func (s *Store) UpdateSettings(ctx context.Context, values map[string]string, now time.Time) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO app_settings(key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for key, value := range values {
		if _, err := stmt.ExecContext(ctx, key, value, millis(now)); err != nil {
			return fmt.Errorf("save setting %s: %w", key, err)
		}
	}
	return tx.Commit()
}
