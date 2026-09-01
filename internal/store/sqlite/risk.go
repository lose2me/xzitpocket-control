package sqlite

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CountLoginAttempts(ctx context.Context, userID, deviceID string, since time.Time) (int, error) {
	var count int
	query := "SELECT COUNT(*) FROM audit_logs WHERE action = 'login_attempt' AND created_at >= ?"
	args := []any{millis(since)}
	if userID != "" {
		query += " AND actor_id = ?"
		args = append(args, userID)
	}
	if deviceID != "" {
		query += " AND target_id = ?"
		args = append(args, deviceID)
	}
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *Store) CreateRiskEvent(ctx context.Context, event RiskEvent) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO risk_events(id, type, user_id, device_id, observed_count, window_start, window_end, detail_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		event.ID, event.Type, nullableString(event.UserID), nullableString(event.DeviceID), event.ObservedCount,
		millis(event.WindowStart), millis(event.WindowEnd), event.Detail, millis(event.CreatedAt))
	return err
}

func (s *Store) ListRiskEvents(ctx context.Context, limit, offset int, acknowledged *bool) ([]RiskEvent, error) {
	query := "SELECT id, type, user_id, device_id, observed_count, window_start, window_end, detail_json, created_at, acknowledged_at FROM risk_events"
	var args []any
	if acknowledged != nil {
		if *acknowledged {
			query += " WHERE acknowledged_at IS NOT NULL"
		} else {
			query += " WHERE acknowledged_at IS NULL"
		}
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RiskEvent
	for rows.Next() {
		var event RiskEvent
		var userID, deviceID, detail sql.NullString
		var start, end, created int64
		var acknowledgedAt sql.NullInt64
		if err := rows.Scan(&event.ID, &event.Type, &userID, &deviceID, &event.ObservedCount,
			&start, &end, &detail, &created, &acknowledgedAt); err != nil {
			return nil, err
		}
		event.UserID = userID.String
		event.DeviceID = deviceID.String
		event.Detail = detail.String
		event.WindowStart = fromMillis(start)
		event.WindowEnd = fromMillis(end)
		event.CreatedAt = fromMillis(created)
		event.AcknowledgedAt = nullableTime(acknowledgedAt)
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) AcknowledgeRiskEvent(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE risk_events SET acknowledged_at = ? WHERE id = ? AND acknowledged_at IS NULL",
		millis(now), id)
	return err
}
