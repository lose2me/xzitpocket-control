package sqlite

import (
	"context"
	"time"
)

type ErrorReportInput struct {
	EventID       string
	UserID        string
	DeviceID      string
	StudentIDHash string
	AppVersion    string
	Platform      string
	Title         string
	Message       string
	ErrorText     string
	StackTrace    string
	OccurredAt    time.Time
	ReceivedAt    time.Time
}

type ErrorReport struct {
	ID            int64
	EventID       string
	UserID        string
	DeviceID      string
	StudentIDHash string
	AppVersion    string
	Platform      string
	Title         string
	Message       string
	ErrorText     string
	StackTrace    string
	OccurredAt    time.Time
	ReceivedAt    time.Time
}

func (s *Store) InsertErrorReport(ctx context.Context, input ErrorReportInput) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO error_reports
		(event_id, user_id, device_id, student_id_hash, app_version, platform, title, message, error_text, stack_trace, occurred_at, received_at)
		VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.EventID, input.UserID, input.DeviceID, input.StudentIDHash,
		input.AppVersion, input.Platform, input.Title, input.Message, input.ErrorText, input.StackTrace,
		millis(input.OccurredAt), millis(input.ReceivedAt))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) CleanupErrorReports(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM error_reports WHERE occurred_at < ?", millis(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) ListErrorReports(ctx context.Context, limit, offset int) ([]ErrorReport, int, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, event_id, COALESCE(user_id, ''), device_id, student_id_hash,
		app_version, platform, title, message, error_text, stack_trace, occurred_at, received_at
		FROM error_reports ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]ErrorReport, 0)
	for rows.Next() {
		item, err := scanErrorReport(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM error_reports").Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func scanErrorReport(row scanner) (ErrorReport, error) {
	var item ErrorReport
	var occurred, received int64
	if err := row.Scan(&item.ID, &item.EventID, &item.UserID, &item.DeviceID, &item.StudentIDHash,
		&item.AppVersion, &item.Platform, &item.Title, &item.Message, &item.ErrorText, &item.StackTrace,
		&occurred, &received); err != nil {
		return ErrorReport{}, err
	}
	item.OccurredAt = fromMillis(occurred)
	item.ReceivedAt = fromMillis(received)
	return item, nil
}
