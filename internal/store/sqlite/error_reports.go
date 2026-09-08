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
	Ignored       bool
}

func (s *Store) InsertErrorReport(ctx context.Context, input ErrorReportInput) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO error_reports
		(event_id, user_id, device_id, student_id_hash, app_version, platform, title, message, error_text, stack_trace, occurred_at, received_at)
		SELECT ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM error_report_ignored_students WHERE student_id_hash = ?
		)`,
		input.EventID, input.UserID, input.DeviceID, input.StudentIDHash,
		input.AppVersion, input.Platform, input.Title, input.Message, input.ErrorText, input.StackTrace,
		millis(input.OccurredAt), millis(input.ReceivedAt), input.StudentIDHash)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// SetErrorReportStudentIgnoredByReportID changes whether a student's future
// error reports are stored. Existing reports remain available to administrators.
func (s *Store) SetErrorReportStudentIgnoredByReportID(ctx context.Context, reportID int64, ignored bool, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var studentIDHash string
	if err := tx.QueryRowContext(ctx, "SELECT student_id_hash FROM error_reports WHERE id = ?", reportID).Scan(&studentIDHash); err != nil {
		return err
	}
	if ignored {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO error_report_ignored_students(student_id_hash, ignored_at)
			VALUES (?, ?)`, studentIDHash, millis(now))
	} else {
		_, err = tx.ExecContext(ctx, "DELETE FROM error_report_ignored_students WHERE student_id_hash = ?", studentIDHash)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ClearErrorReports removes all retained error reports and their associated
// ignore settings so every student can report errors again.
func (s *Store) ClearErrorReports(ctx context.Context) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, "DELETE FROM error_reports")
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM error_report_ignored_students"); err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
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
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id, r.event_id, COALESCE(r.user_id, ''), r.device_id, r.student_id_hash,
		r.app_version, r.platform, r.title, r.message, r.error_text, r.stack_trace, r.occurred_at, r.received_at,
		EXISTS(SELECT 1 FROM error_report_ignored_students i WHERE i.student_id_hash = r.student_id_hash)
		FROM error_reports r ORDER BY r.occurred_at DESC, r.id DESC LIMIT ? OFFSET ?`, limit, offset)
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
		&occurred, &received, &item.Ignored); err != nil {
		return ErrorReport{}, err
	}
	item.OccurredAt = fromMillis(occurred)
	item.ReceivedAt = fromMillis(received)
	return item, nil
}
