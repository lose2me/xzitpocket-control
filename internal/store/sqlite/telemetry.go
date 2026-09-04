package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (s *Store) InsertEvents(ctx context.Context, events []EventInput) (accepted, duplicates int, err error) {
	if len(events) == 0 {
		return 0, 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.PrepareContext(ctx,
		"INSERT OR IGNORE INTO activity_events(event_id, user_id, device_id, type, occurred_at, received_at, properties_json) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()
	for _, event := range events {
		result, execErr := stmt.ExecContext(ctx, event.EventID, nullableString(event.UserID), event.DeviceID,
			event.Type, millis(event.OccurredAt), millis(event.ReceivedAt), event.Properties)
		if execErr != nil {
			return 0, 0, execErr
		}
		count, countErr := result.RowsAffected()
		if countErr != nil {
			return 0, 0, countErr
		}
		if count == 1 {
			accepted++
		} else {
			duplicates++
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return accepted, duplicates, nil
}

func (s *Store) CleanupEvents(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.DB.ExecContext(ctx,
		"DELETE FROM activity_events WHERE occurred_at < ?", millis(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type Overview struct {
	DAU           int `json:"dau"`
	WAU           int `json:"wau"`
	MAU           int `json:"mau"`
	ActiveDevices int `json:"active_devices"`
	TotalUsers    int `json:"total_users"`
	TotalDevices  int `json:"total_devices"`
	TodayEvents   int `json:"today_events"`
}

func (s *Store) MetricsOverview(ctx context.Context, now time.Time) (Overview, error) {
	var result Overview
	now = now.UTC()
	nowMs := millis(now)
	dayStartTime := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayStart := millis(dayStartTime)
	weekStart := millis(dayStartTime.AddDate(0, 0, -6))
	monthStart := millis(dayStartTime.AddDate(0, 0, -29))
	activeTypes := "('app_start','foreground','heartbeat','control_login_success','library_open')"
	query := "SELECT COUNT(DISTINCT user_id) FROM activity_events WHERE user_id IS NOT NULL AND occurred_at >= ? AND occurred_at <= ? AND type IN " + activeTypes
	if err := s.DB.QueryRowContext(ctx, query, dayStart, nowMs).Scan(&result.DAU); err != nil {
		return Overview{}, err
	}
	if err := s.DB.QueryRowContext(ctx, query, weekStart, nowMs).Scan(&result.WAU); err != nil {
		return Overview{}, err
	}
	if err := s.DB.QueryRowContext(ctx, query, monthStart, nowMs).Scan(&result.MAU); err != nil {
		return Overview{}, err
	}
	deviceQuery := "SELECT COUNT(DISTINCT device_id) FROM activity_events WHERE occurred_at >= ? AND occurred_at <= ? AND type IN " + activeTypes
	if err := s.DB.QueryRowContext(ctx, deviceQuery, dayStart, nowMs).Scan(&result.ActiveDevices); err != nil {
		return Overview{}, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE status = 'active'").Scan(&result.TotalUsers); err != nil {
		return Overview{}, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devices WHERE revoked_at IS NULL").Scan(&result.TotalDevices); err != nil {
		return Overview{}, err
	}
	if err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM activity_events WHERE occurred_at >= ? AND occurred_at <= ?", dayStart, nowMs).Scan(&result.TodayEvents); err != nil {
		return Overview{}, err
	}
	return result, nil
}

type SeriesPoint struct {
	Day     string `json:"day"`
	Users   int    `json:"users"`
	Devices int    `json:"devices"`
	Events  int    `json:"events"`
}

type MetricsBreakdown struct {
	Platforms           map[string]int `json:"platforms"`
	Versions            map[string]int `json:"versions"`
	LibraryEntries      int            `json:"library_entries"`
	LibraryUsers        int            `json:"library_users"`
	AnonymousEvents     int            `json:"anonymous_events"`
	NewDevices          int            `json:"new_devices"`
	NewUsers            int            `json:"new_users"`
	CDKActivationsToday int            `json:"cdk_activations_today"`
	CDKActivationsWeek  int            `json:"cdk_activations_week"`
	CDKActivationsTotal int            `json:"cdk_activations_total"`
}

func (s *Store) MetricsBreakdown(ctx context.Context, now time.Time) (MetricsBreakdown, error) {
	result := MetricsBreakdown{Platforms: map[string]int{}, Versions: map[string]int{}}
	now = now.UTC()
	start := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	deviceRows, err := s.DB.QueryContext(ctx, `SELECT platform, app_version, COUNT(*)
		FROM devices WHERE revoked_at IS NULL GROUP BY platform, app_version`)
	if err != nil {
		return result, err
	}
	defer deviceRows.Close()
	for deviceRows.Next() {
		var platform, version string
		var count int
		if err := deviceRows.Scan(&platform, &version, &count); err != nil {
			return result, err
		}
		if platform != "" {
			result.Platforms[platform] += count
		}
		if version != "" {
			result.Versions[version] += count
		}
	}
	if err := deviceRows.Err(); err != nil {
		return result, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT user_id, type, properties_json FROM activity_events WHERE occurred_at >= ? AND occurred_at <= ?", millis(start), millis(now))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	libraryUsers := map[string]bool{}
	activeType := map[string]bool{"app_start": true, "foreground": true, "heartbeat": true, "control_login_success": true, "library_open": true}
	for rows.Next() {
		var userID sql.NullString
		var eventType, raw string
		if err := rows.Scan(&userID, &eventType, &raw); err != nil {
			return result, err
		}
		var props map[string]any
		if json.Unmarshal([]byte(raw), &props) != nil {
			continue
		}
		if !userID.Valid && activeType[eventType] {
			result.AnonymousEvents++
		}
		if eventType == "library_open" {
			result.LibraryEntries++
			if userID.Valid && userID.String != "" {
				libraryUsers[userID.String] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.LibraryUsers = len(libraryUsers)
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devices WHERE created_at >= ? AND created_at <= ?", millis(start), millis(now)).Scan(&result.NewDevices); err != nil {
		return result, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE created_at >= ? AND created_at <= ?", millis(start), millis(now)).Scan(&result.NewUsers); err != nil {
		return result, err
	}
	// CDK activation means a successful redemption. "This week" uses the
	// calendar week beginning Monday in UTC, matching the dashboard's date
	// boundaries elsewhere in this package.
	weekStart := start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7))
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM library_cdks WHERE status = 'used' AND used_at IS NOT NULL AND used_at >= ? AND used_at <= ?", millis(start), millis(now)).Scan(&result.CDKActivationsToday); err != nil {
		return result, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM library_cdks WHERE status = 'used' AND used_at IS NOT NULL AND used_at >= ? AND used_at <= ?", millis(weekStart), millis(now)).Scan(&result.CDKActivationsWeek); err != nil {
		return result, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM library_cdks WHERE status = 'used' AND used_at IS NOT NULL").Scan(&result.CDKActivationsTotal); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) MetricsSeries(ctx context.Context, days int, now time.Time) ([]SeriesPoint, error) {
	if days < 1 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
	rows, err := s.DB.QueryContext(ctx,
		"SELECT date(occurred_at / 1000, 'unixepoch') AS day, COUNT(DISTINCT user_id), COUNT(DISTINCT device_id), COUNT(*) FROM activity_events WHERE occurred_at >= ? GROUP BY day ORDER BY day",
		millis(start))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]SeriesPoint{}
	for rows.Next() {
		var point SeriesPoint
		if err := rows.Scan(&point.Day, &point.Users, &point.Devices, &point.Events); err != nil {
			return nil, err
		}
		byDay[point.Day] = point
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]SeriesPoint, 0, days)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		key := day.Format("2006-01-02")
		if point, ok := byDay[key]; ok {
			result = append(result, point)
		} else {
			result = append(result, SeriesPoint{Day: key})
		}
	}
	return result, nil
}
