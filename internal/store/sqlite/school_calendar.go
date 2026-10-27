package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// SchoolCalendarConfig contains the normalized JSON payload returned to app
// clients. The row is a singleton and is always stored with id = 1.
//
// DaysJSON is the calendar days array; SlotsJSON is the timetable section
// (作息时间) array served alongside it so app clients stop hardcoding periods.
type SchoolCalendarConfig struct {
	DaysJSON  string
	SlotsJSON string
	UpdatedAt time.Time
}

func (s *Store) GetSchoolCalendarConfig(ctx context.Context) (SchoolCalendarConfig, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT days_json, slots_json, updated_at FROM school_calendar_config WHERE id = 1")
	var config SchoolCalendarConfig
	var updatedAt int64
	if err := row.Scan(&config.DaysJSON, &config.SlotsJSON, &updatedAt); err != nil {
		return SchoolCalendarConfig{}, err
	}
	if updatedAt > 0 {
		config.UpdatedAt = fromMillis(updatedAt)
	}
	return config, nil
}

// SchoolCalendarCollege is one college's calendar override.
type SchoolCalendarCollege struct {
	College   string
	DaysJSON  string
	UpdatedAt time.Time
}

// GetSchoolCalendarCollege returns a college override; ok is false when the
// college uses the global calendar.
func (s *Store) GetSchoolCalendarCollege(ctx context.Context, college string) (SchoolCalendarCollege, bool, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT college, days_json, updated_at FROM school_calendar_colleges WHERE college = ?", strings.TrimSpace(college))
	var item SchoolCalendarCollege
	var updatedAt int64
	if err := row.Scan(&item.College, &item.DaysJSON, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return SchoolCalendarCollege{}, false, nil
	} else if err != nil {
		return SchoolCalendarCollege{}, false, err
	}
	if updatedAt > 0 {
		item.UpdatedAt = fromMillis(updatedAt)
	}
	return item, true, nil
}

// ListSchoolCalendarColleges returns the colleges that have an override.
func (s *Store) ListSchoolCalendarColleges(ctx context.Context) ([]SchoolCalendarCollege, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT college, days_json, updated_at FROM school_calendar_colleges ORDER BY college")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SchoolCalendarCollege, 0)
	for rows.Next() {
		var item SchoolCalendarCollege
		var updatedAt int64
		if err := rows.Scan(&item.College, &item.DaysJSON, &updatedAt); err != nil {
			return nil, err
		}
		if updatedAt > 0 {
			item.UpdatedAt = fromMillis(updatedAt)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SaveSchoolCalendarCollege upserts one college override.
func (s *Store) SaveSchoolCalendarCollege(ctx context.Context, college, daysJSON string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO school_calendar_colleges(college, days_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(college) DO UPDATE SET days_json = excluded.days_json, updated_at = excluded.updated_at`,
		strings.TrimSpace(college), daysJSON, millis(now))
	return err
}

// DeleteSchoolCalendarCollege drops one college override.
func (s *Store) DeleteSchoolCalendarCollege(ctx context.Context, college string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM school_calendar_colleges WHERE college = ?", strings.TrimSpace(college))
	return err
}

// ListUserColleges returns the distinct non-empty college names seen in user
// profiles, so the console can offer them as options.
func (s *Store) ListUserColleges(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT DISTINCT college_name FROM users WHERE TRIM(college_name) <> '' ORDER BY college_name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSchoolCalendarConfig(ctx context.Context, config SchoolCalendarConfig) (SchoolCalendarConfig, error) {
	var updatedAt int64
	err := s.DB.QueryRowContext(ctx,
		`UPDATE school_calendar_config SET days_json = ?, slots_json = ?, updated_at = MAX(updated_at + 1, ?)
		WHERE id = 1 RETURNING updated_at`,
		config.DaysJSON, config.SlotsJSON, millis(config.UpdatedAt)).Scan(&updatedAt)
	if err != nil {
		return SchoolCalendarConfig{}, err
	}
	config.UpdatedAt = fromMillis(updatedAt)
	return config, nil
}
