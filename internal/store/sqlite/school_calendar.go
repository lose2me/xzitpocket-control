package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// SchoolCalendarConfig contains the normalized JSON payload returned to app
// clients. The row is a singleton and is always stored with id = 1.
type SchoolCalendarConfig struct {
	DaysJSON  string
	UpdatedAt time.Time
}

func (s *Store) GetSchoolCalendarConfig(ctx context.Context) (SchoolCalendarConfig, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT days_json, updated_at FROM school_calendar_config WHERE id = 1")
	var config SchoolCalendarConfig
	var updatedAt int64
	if err := row.Scan(&config.DaysJSON, &updatedAt); err != nil {
		return SchoolCalendarConfig{}, err
	}
	if updatedAt > 0 {
		config.UpdatedAt = fromMillis(updatedAt)
	}
	return config, nil
}

func (s *Store) UpdateSchoolCalendarConfig(ctx context.Context, config SchoolCalendarConfig) error {
	result, err := s.DB.ExecContext(ctx,
		"UPDATE school_calendar_config SET days_json = ?, updated_at = ? WHERE id = 1",
		config.DaysJSON, millis(config.UpdatedAt))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
