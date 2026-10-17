package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Submission is a PDF an app user uploaded for administrator review. An
// approved submission becomes a textbook or resource row that keeps PdfKey;
// reviewed rows are removed from this queue.
type Submission struct {
	ID                  string    `json:"id"`
	Kind                string    `json:"kind"`
	CourseCode          string    `json:"course_code"`
	Name                string    `json:"name"`
	Tags                string    `json:"tags"`
	PdfKey              string    `json:"pdf_key"`
	SizeBytes           int64     `json:"size_bytes"`
	Status              string    `json:"status"`
	SubmittedByUserID   string    `json:"submitted_by_user_id"`
	SubmittedByName     string    `json:"submitted_by_name"`
	StudentIDCiphertext string    `json:"-"`
	TargetID            string    `json:"target_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type SubmissionInput struct {
	Kind                string
	CourseCode          string
	Name                string
	Tags                string
	PdfKey              string
	SizeBytes           int64
	Status              string
	SubmittedByUserID   string
	SubmittedByName     string
	StudentIDCiphertext string
	TargetID            string
}

const submissionColumns = "id, kind, course_code, name, tags, pdf_key, size_bytes, status, submitted_by_user_id, submitted_by_name, student_id_ciphertext, target_id, created_at, updated_at"

func (s *Store) CreateSubmission(ctx context.Context, input SubmissionInput, now time.Time) (Submission, error) {
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "pending_review"
	}
	id := newStoreIDPrefix("sub")
	_, err := s.DB.ExecContext(ctx, `INSERT INTO resource_submissions(
			id, kind, course_code, name, tags, pdf_key, size_bytes, status, submitted_by_user_id, submitted_by_name, student_id_ciphertext, target_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.Kind, input.CourseCode, input.Name, input.Tags, input.PdfKey, input.SizeBytes, input.Status,
		input.SubmittedByUserID, input.SubmittedByName, input.StudentIDCiphertext, input.TargetID, millis(now), millis(now))
	if err != nil {
		return Submission{}, err
	}
	return s.GetSubmission(ctx, id)
}

func (s *Store) ListSubmissions(ctx context.Context, kind string, limit, offset int) ([]Submission, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	where := " WHERE status = 'pending_review'"
	args := make([]any, 0, 3)
	if kind = strings.TrimSpace(kind); kind != "" {
		where += " AND kind = ?"
		args = append(args, kind)
	}
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+submissionColumns+" FROM resource_submissions"+where+
		" ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Submission, 0)
	for rows.Next() {
		item, scanErr := scanSubmission(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CountSubmissions(ctx context.Context, kind string) (int, error) {
	where := " WHERE status = 'pending_review'"
	args := []any{}
	if kind = strings.TrimSpace(kind); kind != "" {
		where += " AND kind = ?"
		args = append(args, kind)
	}
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM resource_submissions"+where, args...).Scan(&count)
	return count, err
}

// CountSubmissionsByUser bounds how many unreviewed uploads one account can
// keep in the queue at once.
func (s *Store) CountSubmissionsByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM resource_submissions WHERE submitted_by_user_id = ? AND status = 'pending_review'",
		strings.TrimSpace(userID)).Scan(&count)
	return count, err
}

// CountUploadingSubmissionsByUser limits concurrent direct uploads per account.
func (s *Store) CountUploadingSubmissionsByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM resource_submissions WHERE submitted_by_user_id = ? AND status = 'pending_upload'",
		strings.TrimSpace(userID)).Scan(&count)
	return count, err
}

// ListStaleSubmissions returns upload tickets whose direct upload never
// completed, so the caller can delete their objects and rows.
func (s *Store) ListStaleSubmissions(ctx context.Context, before time.Time, limit int) ([]Submission, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+submissionColumns+` FROM resource_submissions
		WHERE status = 'pending_upload' AND created_at < ? ORDER BY created_at ASC LIMIT ?`,
		millis(before), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Submission, 0)
	for rows.Next() {
		item, scanErr := scanSubmission(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetSubmission(ctx context.Context, id string) (Submission, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT "+submissionColumns+" FROM resource_submissions WHERE id = ?", id)
	item, err := scanSubmission(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, sql.ErrNoRows
	}
	return item, err
}

func (s *Store) SetSubmissionPdfKey(ctx context.Context, id, key string, sizeBytes int64, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE resource_submissions SET pdf_key = ?, size_bytes = ?, updated_at = ? WHERE id = ?`,
		key, sizeBytes, millis(now), id)
	if err != nil {
		return err
	}
	if affected, countErr := result.RowsAffected(); countErr != nil {
		return countErr
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// MarkSubmissionUploaded promotes a verified direct upload into the review queue.
func (s *Store) MarkSubmissionUploaded(ctx context.Context, id string, sizeBytes int64, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE resource_submissions
		SET status = 'pending_review', size_bytes = ?, updated_at = ? WHERE id = ? AND status = 'pending_upload'`,
		sizeBytes, millis(now), id)
	if err != nil {
		return err
	}
	if affected, countErr := result.RowsAffected(); countErr != nil {
		return countErr
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteUploadingSubmission removes an upload ticket only while it is still
// waiting for its file, so a concurrent completion is never deleted.
func (s *Store) DeleteUploadingSubmission(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM resource_submissions WHERE id = ? AND status = 'pending_upload'", id)
	if err != nil {
		return err
	}
	if affected, countErr := result.RowsAffected(); countErr != nil {
		return countErr
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteSubmission(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM resource_submissions WHERE id = ?", id)
	if err != nil {
		return err
	}
	if affected, countErr := result.RowsAffected(); countErr != nil {
		return countErr
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// PdfKeyInUse reports whether an approved submission's object is already
// referenced by content, so rejecting a leftover queue row never deletes a
// file that the resource centre still serves.
func (s *Store) PdfKeyInUse(ctx context.Context, key string) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, nil
	}
	var exists int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM textbooks WHERE pdf_key = ?
		UNION ALL SELECT 1 FROM resources WHERE pdf_key = ? LIMIT 1`, key, key).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

type rowScannerSubmission interface {
	Scan(dest ...any) error
}

func scanSubmission(row rowScannerSubmission) (Submission, error) {
	var item Submission
	var userID sql.NullString
	var created, updated int64
	if err := row.Scan(&item.ID, &item.Kind, &item.CourseCode, &item.Name, &item.Tags, &item.PdfKey,
		&item.SizeBytes, &item.Status, &userID, &item.SubmittedByName, &item.StudentIDCiphertext, &item.TargetID, &created, &updated); err != nil {
		return Submission{}, err
	}
	item.SubmittedByUserID = userID.String
	item.CreatedAt = fromMillis(created)
	item.UpdatedAt = fromMillis(updated)
	return item, nil
}
