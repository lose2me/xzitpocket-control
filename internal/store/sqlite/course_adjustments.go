package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// CourseAdjustmentsConfig contains the normalized JSON mapping managed by the
// administrator. The row is a singleton and is always stored with id = 1.
type CourseAdjustmentsConfig struct {
	AdjustmentsJSON string
	UpdatedAt       time.Time
}

func (s *Store) GetCourseAdjustmentsConfig(ctx context.Context) (CourseAdjustmentsConfig, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT adjustments_json, updated_at FROM course_adjustments_config WHERE id = 1")
	var config CourseAdjustmentsConfig
	var updatedAt int64
	if err := row.Scan(&config.AdjustmentsJSON, &updatedAt); err != nil {
		return CourseAdjustmentsConfig{}, err
	}
	if updatedAt > 0 {
		config.UpdatedAt = fromMillis(updatedAt)
	}
	return config, nil
}

func (s *Store) UpdateCourseAdjustmentsConfig(ctx context.Context, config CourseAdjustmentsConfig) error {
	result, err := s.DB.ExecContext(ctx,
		"UPDATE course_adjustments_config SET adjustments_json = ?, updated_at = ? WHERE id = 1",
		config.AdjustmentsJSON, millis(config.UpdatedAt))
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
