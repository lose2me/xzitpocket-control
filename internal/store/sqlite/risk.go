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
	query := "SELECT r.id, r.type, r.user_id, u.display_name, u.class_name, r.device_id, d.app_version, d.revoked_at, r.observed_count, r.window_start, r.window_end, r.detail_json, r.created_at, r.acknowledged_at FROM risk_events r LEFT JOIN users u ON u.id = r.user_id LEFT JOIN devices d ON d.id = r.device_id"
	var args []any
	if acknowledged != nil {
		if *acknowledged {
			query += " WHERE acknowledged_at IS NOT NULL"
		} else {
			query += " WHERE acknowledged_at IS NULL"
		}
	}
	query += " ORDER BY r.created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RiskEvent
	for rows.Next() {
		var event RiskEvent
		var userID, displayName, className, deviceID, detail sql.NullString
		var deviceAppVersion sql.NullString
		var deviceRevokedAt sql.NullInt64
		var start, end, created int64
		var acknowledgedAt sql.NullInt64
		if err := rows.Scan(&event.ID, &event.Type, &userID, &displayName, &className, &deviceID, &deviceAppVersion, &deviceRevokedAt, &event.ObservedCount,
			&start, &end, &detail, &created, &acknowledgedAt); err != nil {
			return nil, err
		}
		event.UserID = userID.String
		event.DisplayName = displayName.String
		event.ClassName = className.String
		event.DeviceID = deviceID.String
		event.DeviceAppVersion = deviceAppVersion.String
		event.DeviceRevokedAt = nullableTime(deviceRevokedAt)
		event.Detail = detail.String
		event.WindowStart = fromMillis(start)
		event.WindowEnd = fromMillis(end)
		event.CreatedAt = fromMillis(created)
		event.AcknowledgedAt = nullableTime(acknowledgedAt)
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) ClearRiskEvents(ctx context.Context) (int64, error) {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM risk_events")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) AcknowledgeRiskEvent(ctx context.Context, id string, now time.Time) error {
	result, err := s.DB.ExecContext(ctx,
		"UPDATE risk_events SET acknowledged_at = ? WHERE id = ? AND acknowledged_at IS NULL",
		millis(now), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
