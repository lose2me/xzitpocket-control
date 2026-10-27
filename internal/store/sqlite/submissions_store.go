package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Submission is one upload an app user sent for administrator review. A
// submit carries one or more files; the console reviews and downloads them as
// a list, so the row is only a container for its files.
type Submission struct {
	ID                  string    `json:"id"`
	Kind                string    `json:"kind"`
	CourseCode          string    `json:"course_code"`
	Name                string    `json:"name"`
	Tags                string    `json:"tags"`
	Note                string    `json:"note"`
	SizeBytes           int64     `json:"size_bytes"`
	Status              string    `json:"status"`
	SubmittedByUserID   string    `json:"submitted_by_user_id"`
	SubmittedByName     string    `json:"submitted_by_name"`
	StudentIDCiphertext string    `json:"-"`
	TargetID            string    `json:"target_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	Files               []SubmissionFile
}

// SubmissionFile is one uploaded file of a submission. duplicate marks content
// that was already published when it was uploaded.
type SubmissionFile struct {
	ID           string    `json:"id"`
	SubmissionID string    `json:"submission_id"`
	Name         string    `json:"name"`
	ObjectKey    string    `json:"object_key"`
	Format       string    `json:"format"`
	SizeBytes    int64     `json:"size_bytes"`
	Duplicate    bool      `json:"duplicate"`
	CreatedAt    time.Time `json:"created_at"`
}

type SubmissionFileInput struct {
	SubmissionID string
	Name         string
	ObjectKey    string
	Format       string
	SizeBytes    int64
	Duplicate    bool
}

type SubmissionInput struct {
	Kind                string
	CourseCode          string
	Name                string
	Tags                string
	Note                string
	SizeBytes           int64
	Status              string
	SubmittedByUserID   string
	SubmittedByName     string
	StudentIDCiphertext string
	TargetID            string
}

const submissionColumns = "id, kind, course_code, name, tags, size_bytes, status, submitted_by_user_id, submitted_by_name, student_id_ciphertext, target_id, created_at, updated_at, note"

const submissionFileColumns = "id, submission_id, name, object_key, format, size_bytes, duplicate, created_at"

func (s *Store) CreateSubmission(ctx context.Context, input SubmissionInput, now time.Time) (Submission, error) {
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "pending_review"
	}
	id := newStoreIDPrefix("sub")
	_, err := s.DB.ExecContext(ctx, `INSERT INTO resource_submissions(
			id, kind, course_code, name, tags, size_bytes, status, submitted_by_user_id, submitted_by_name, student_id_ciphertext, target_id, created_at, updated_at, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.Kind, input.CourseCode, input.Name, input.Tags, input.SizeBytes, input.Status,
		input.SubmittedByUserID, input.SubmittedByName, input.StudentIDCiphertext, input.TargetID, millis(now), millis(now), input.Note)
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
	where := " WHERE status = 'pending_review' AND EXISTS (SELECT 1 FROM submission_files f WHERE f.submission_id = resource_submissions.id)"
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachSubmissionFiles(ctx, items)
}

func (s *Store) CountSubmissions(ctx context.Context, kind string) (int, error) {
	where := " WHERE status = 'pending_review' AND EXISTS (SELECT 1 FROM submission_files f WHERE f.submission_id = resource_submissions.id)"
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachSubmissionFiles(ctx, items)
}

func (s *Store) GetSubmission(ctx context.Context, id string) (Submission, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT "+submissionColumns+" FROM resource_submissions WHERE id = ?", id)
	item, err := scanSubmission(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, sql.ErrNoRows
	}
	if err != nil {
		return Submission{}, err
	}
	files, err := s.ListSubmissionFiles(ctx, []string{item.ID})
	if err != nil {
		return Submission{}, err
	}
	item.Files = files[item.ID]
	return item, nil
}

// attachSubmissionFiles fills Files for a page of submissions in one query.
func (s *Store) attachSubmissionFiles(ctx context.Context, items []Submission) ([]Submission, error) {
	if len(items) == 0 {
		return items, nil
	}
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	files, err := s.ListSubmissionFiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Files = files[items[i].ID]
	}
	return items, nil
}

// AddSubmissionFile reserves one file row for a submission ticket.
func (s *Store) AddSubmissionFile(ctx context.Context, input SubmissionFileInput, now time.Time) (SubmissionFile, error) {
	id := newStoreIDPrefix("sf")
	duplicate := 0
	if input.Duplicate {
		duplicate = 1
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO submission_files(
			id, submission_id, name, object_key, format, size_bytes, duplicate, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.SubmissionID, input.Name, input.ObjectKey, input.Format, input.SizeBytes, duplicate, millis(now)); err != nil {
		return SubmissionFile{}, err
	}
	return s.GetSubmissionFile(ctx, id)
}

// GetSubmissionFile returns one file row by id.
func (s *Store) GetSubmissionFile(ctx context.Context, id string) (SubmissionFile, error) {
	item, err := scanSubmissionFile(s.DB.QueryRowContext(ctx, "SELECT "+submissionFileColumns+" FROM submission_files WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return SubmissionFile{}, sql.ErrNoRows
	}
	return item, err
}

// ListSubmissionFiles returns the files of several submissions grouped by
// submission id.
func (s *Store) ListSubmissionFiles(ctx context.Context, submissionIDs []string) (map[string][]SubmissionFile, error) {
	out := make(map[string][]SubmissionFile, len(submissionIDs))
	if len(submissionIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(submissionIDs)), ",")
	args := make([]any, len(submissionIDs))
	for i, id := range submissionIDs {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+submissionFileColumns+" FROM submission_files WHERE submission_id IN ("+
		placeholders+") ORDER BY created_at ASC, id ASC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanSubmissionFile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out[item.SubmissionID] = append(out[item.SubmissionID], item)
	}
	return out, rows.Err()
}

// UpdateSubmissionFileSize stores the size the object reported after upload.
func (s *Store) UpdateSubmissionFileSize(ctx context.Context, id string, sizeBytes int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE submission_files SET size_bytes = ? WHERE id = ?`, sizeBytes, id)
	return err
}

// DeleteSubmissionFile removes one file row.
func (s *Store) DeleteSubmissionFile(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM submission_files WHERE id = ?`, id)
	return err
}

// CountSubmissionFileKeyRefs reports how many files still point at the same
// object key, so deleting one submission never deletes a shared object.
func (s *Store) CountSubmissionFileKeyRefs(ctx context.Context, key string) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM submission_files WHERE object_key = ?`, strings.TrimSpace(key)).Scan(&count)
	return count, err
}

// CountSubmissionFiles reports how many files a submission still has.
func (s *Store) CountSubmissionFiles(ctx context.Context, submissionID string) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM submission_files WHERE submission_id = ?`, submissionID).Scan(&count)
	return count, err
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

// PdfKeyInUse reports whether an object is still referenced by published
// content or another pending submission. Content-addressed keys can be shared
// by identical files, so cleanup must never delete an object another row uses.
func (s *Store) PdfKeyInUse(ctx context.Context, key string) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, nil
	}
	var exists int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM textbooks WHERE pdf_key = ?
		UNION ALL SELECT 1 FROM resources WHERE pdf_key = ?
		UNION ALL SELECT 1 FROM submission_files WHERE object_key = ? LIMIT 1`,
		key, key, key).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CoverKeyInUse reports whether another textbook still references the cover,
// which is possible when two textbooks point at the same PDF.
func (s *Store) CoverKeyInUse(ctx context.Context, key string) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, nil
	}
	var exists int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM textbooks WHERE cover_key = ? LIMIT 1`, key).Scan(&exists)
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
	if err := row.Scan(&item.ID, &item.Kind, &item.CourseCode, &item.Name, &item.Tags,
		&item.SizeBytes, &item.Status, &userID, &item.SubmittedByName, &item.StudentIDCiphertext,
		&item.TargetID, &created, &updated, &item.Note); err != nil {
		return Submission{}, err
	}
	item.SubmittedByUserID = userID.String
	item.CreatedAt = fromMillis(created)
	item.UpdatedAt = fromMillis(updated)
	return item, nil
}

func scanSubmissionFile(row rowScannerSubmission) (SubmissionFile, error) {
	var item SubmissionFile
	var duplicate int
	var created int64
	if err := row.Scan(&item.ID, &item.SubmissionID, &item.Name, &item.ObjectKey, &item.Format,
		&item.SizeBytes, &duplicate, &created); err != nil {
		return SubmissionFile{}, err
	}
	item.Duplicate = duplicate != 0
	item.CreatedAt = fromMillis(created)
	return item, nil
}
