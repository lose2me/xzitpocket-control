package sqlite

import (
	"context"
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

func (s *Store) RebuildDailyMetrics(ctx context.Context, day, appID string) error {
	if day == "" {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM daily_metrics WHERE day = ? AND app_id = ?", day, appID); err != nil {
		return err
	}
	start, parseErr := time.Parse("2006-01-02", day)
	if parseErr != nil {
		return parseErr
	}
	end := start.AddDate(0, 0, 1)
	activeTypes := "('app_start','foreground','heartbeat','control_login_success','paid_service_open','paid_service_token_success')"
	insert := func(metric string, value int) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO daily_metrics(day, app_id, metric, dimension_json, value) VALUES (?, ?, ?, '{}', ?)", day, appID, metric, value)
		return e
	}
	var value int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(DISTINCT user_id) FROM activity_events WHERE user_id IS NOT NULL AND occurred_at >= ? AND occurred_at < ? AND type IN "+activeTypes, millis(start), millis(end)).Scan(&value); err != nil {
		return err
	}
	if err := insert("active_users", value); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(DISTINCT device_id) FROM activity_events WHERE occurred_at >= ? AND occurred_at < ? AND type IN "+activeTypes, millis(start), millis(end)).Scan(&value); err != nil {
		return err
	}
	if err := insert("active_devices", value); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM activity_events WHERE occurred_at >= ? AND occurred_at < ?", millis(start), millis(end)).Scan(&value); err != nil {
		return err
	}
	if err := insert("events", value); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM activity_events WHERE occurred_at >= ? AND occurred_at < ? AND type = 'paid_service_open'", millis(start), millis(end)).Scan(&value); err != nil {
		return err
	}
	if err := insert("paid_entries", value); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CleanupDailyMetrics(ctx context.Context, beforeDay string) (int64, error) {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM daily_metrics WHERE day < ?", beforeDay)
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
	nowMs := millis(now)
	dayStart := millis(time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC))
	weekStart := millis(now.AddDate(0, 0, -6))
	monthStart := millis(now.AddDate(0, 0, -29))
	activeTypes := "('app_start','foreground','heartbeat','control_login_success','paid_service_open','paid_service_token_success')"
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
	Platforms       map[string]int `json:"platforms"`
	Versions        map[string]int `json:"versions"`
	PaidEntries     int            `json:"paid_entries"`
	PaidUsers       int            `json:"paid_users"`
	AnonymousEvents int            `json:"anonymous_events"`
	NewDevices      int            `json:"new_devices"`
	NewUsers        int            `json:"new_users"`
}

func (s *Store) MetricsBreakdown(ctx context.Context, now time.Time) (MetricsBreakdown, error) {
	result := MetricsBreakdown{Platforms: map[string]int{}, Versions: map[string]int{}}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	rows, err := s.DB.QueryContext(ctx, "SELECT user_id, type, properties_json FROM activity_events WHERE occurred_at >= ?", millis(start))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	paidUsers := map[string]bool{}
	activeType := map[string]bool{"app_start": true, "foreground": true, "heartbeat": true, "control_login_success": true, "paid_service_open": true, "paid_service_token_success": true}
	for rows.Next() {
		var userID, eventType, raw string
		if err := rows.Scan(&userID, &eventType, &raw); err != nil {
			return result, err
		}
		var props map[string]any
		if json.Unmarshal([]byte(raw), &props) != nil {
			continue
		}
		if platform, ok := props["platform"].(string); ok && platform != "" {
			result.Platforms[platform]++
		}
		if version, ok := props["app_version"].(string); ok && version != "" {
			result.Versions[version]++
		}
		if userID == "" && activeType[eventType] {
			result.AnonymousEvents++
		}
		if eventType == "paid_service_open" {
			result.PaidEntries++
			if userID != "" {
				paidUsers[userID] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.PaidUsers = len(paidUsers)
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devices WHERE created_at >= ?", millis(start)).Scan(&result.NewDevices); err != nil {
		return result, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE created_at >= ?", millis(start)).Scan(&result.NewUsers); err != nil {
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
