package sqlite

import (
	"context"
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

func (s *Store) UpdateSchoolCalendarConfig(ctx context.Context, config SchoolCalendarConfig) (SchoolCalendarConfig, error) {
	var updatedAt int64
	err := s.DB.QueryRowContext(ctx,
		`UPDATE school_calendar_config SET days_json = ?, updated_at = MAX(updated_at + 1, ?)
		WHERE id = 1 RETURNING updated_at`,
		config.DaysJSON, millis(config.UpdatedAt)).Scan(&updatedAt)
	if err != nil {
		return SchoolCalendarConfig{}, err
	}
	config.UpdatedAt = fromMillis(updatedAt)
	return config, nil
}
