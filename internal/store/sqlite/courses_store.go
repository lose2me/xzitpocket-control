package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrCourseExists is returned when an administrator creates or renames a
// record onto a course number that is already taken.
var ErrCourseExists = errors.New("course already exists")

// Course is one uploaded course record. The course number (Code) identifies it.
type Course struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CourseInput struct {
	Code string
	Name string
}

const courseColumns = "id, code, name, created_at, updated_at"

// UploadCourses inserts every unseen course and completes an empty name of an
// existing course. Existing values are never overwritten.
func (s *Store) UploadCourses(ctx context.Context, inputs []CourseInput, now time.Time) (int, error) {
	if len(inputs) == 0 {
		return 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var accepted int
	for _, input := range inputs {
		if strings.TrimSpace(input.Code) == "" {
			continue
		}
		id, err := newStoreID("crs")
		if err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO courses(id, code, name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
			id, input.Code, input.Name, millis(now), millis(now))
		if err == nil {
			if affected, countErr := result.RowsAffected(); countErr == nil && affected == 1 {
				accepted++
			}
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, err
		}
		// The course already exists: only complete an empty name.
		if _, err := tx.ExecContext(ctx, `UPDATE courses SET
			name = CASE WHEN name = '' THEN ? ELSE name END,
			updated_at = CASE WHEN name = '' AND ? <> '' THEN ? ELSE updated_at END
			WHERE code = ?`,
			input.Name, input.Name, millis(now), input.Code); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return accepted, nil
}

// courseListSorts whitelists the ORDER BY targets so only these fixed strings
// ever reach the SQL text.
var courseListSorts = map[string]string{
	"code":           "c.code",
	"name":           "c.name",
	"created_at":     "c.created_at",
	"updated_at":     "c.updated_at",
	"resource_count": "(SELECT COUNT(*) FROM resources r WHERE r.course_code = c.code)",
}

const courseDefaultOrderBy = "c.name ASC, c.id"

func courseOrderBy(sort, order string) string {
	column, ok := courseListSorts[strings.TrimSpace(sort)]
	if !ok {
		return courseDefaultOrderBy
	}
	direction := "ASC"
	if strings.EqualFold(strings.TrimSpace(order), "desc") {
		direction = "DESC"
	}
	return column + " " + direction + ", c.id"
}

func courseWhere(search string) (string, []any) {
	if search = strings.TrimSpace(search); search == "" {
		return "", nil
	}
	like := "%" + escapeLike(search) + "%"
	return " WHERE (c.code LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\')",
		[]any{like, like}
}

func (s *Store) ListCourses(ctx context.Context, limit, offset int, search, sort, order string) ([]Course, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	where, args := courseWhere(search)
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+courseColumns+" FROM courses c"+where+
		" ORDER BY "+courseOrderBy(sort, order)+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Course, 0)
	for rows.Next() {
		item, scanErr := scanCourse(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CountCourses(ctx context.Context, search string) (int, error) {
	where, args := courseWhere(search)
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM courses c"+where, args...).Scan(&count)
	return count, err
}

// CourseCodeExists reports whether a course number is known. App submissions
// reference courses by code and must not create associations to unknown codes.
func (s *Store) CourseCodeExists(ctx context.Context, code string) (bool, error) {
	var exists int
	err := s.DB.QueryRowContext(ctx, "SELECT 1 FROM courses WHERE code = ?", strings.TrimSpace(code)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) GetCourse(ctx context.Context, id string) (Course, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT "+courseColumns+" FROM courses WHERE id = ?", id)
	item, err := scanCourse(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Course{}, sql.ErrNoRows
	}
	return item, err
}

// CreateCourse inserts an administrator-authored record. A duplicate code is
// reported as a conflict.
func (s *Store) CreateCourse(ctx context.Context, input CourseInput, now time.Time) (Course, error) {
	id, err := newStoreID("crs")
	if err != nil {
		return Course{}, err
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO courses(id, code, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, input.Code, input.Name, millis(now), millis(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Course{}, ErrCourseExists
		}
		return Course{}, err
	}
	if affected, countErr := result.RowsAffected(); countErr == nil && affected != 1 {
		return Course{}, ErrCourseExists
	}
	return s.GetCourse(ctx, id)
}

func (s *Store) UpdateCourse(ctx context.Context, id string, input CourseInput, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE courses SET code = ?, name = ?, updated_at = ? WHERE id = ?`,
		input.Code, input.Name, millis(now), id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrCourseExists
		}
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

// RenameCourseCode moves every association from one course number to another
// inside a single transaction. Course numbers are the join key for textbooks,
// resources, question banks and submissions, so renaming without this would
// detach all of their content. Affected rows bump updated_at so clients see a
// new version and reload.
func (s *Store) RenameCourseCode(ctx context.Context, oldCode, newCode string, now time.Time) error {
	oldCode, newCode = strings.TrimSpace(oldCode), strings.TrimSpace(newCode)
	if oldCode == "" || newCode == "" || oldCode == newCode {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stamp := millis(now)
	// Multi-course joins first: a row may already be linked to both codes.
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO textbook_courses(textbook_id, course_code)
		SELECT textbook_id, ? FROM textbook_courses WHERE course_code = ?`, newCode, oldCode); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM textbook_courses WHERE course_code = ?`, oldCode); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO question_bank_courses(question_bank_id, course_code)
		SELECT question_bank_id, ? FROM question_bank_courses WHERE course_code = ?`, newCode, oldCode); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM question_bank_courses WHERE course_code = ?`, oldCode); err != nil {
		return err
	}
	for _, statement := range []string{
		`UPDATE textbooks SET course_code = ?, updated_at = ? WHERE course_code = ?`,
		`UPDATE resources SET course_code = ?, updated_at = ? WHERE course_code = ?`,
		`UPDATE question_banks SET course_code = ?, updated_at = ? WHERE course_code = ?`,
		`UPDATE resource_submissions SET course_code = ?, updated_at = ? WHERE course_code = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, newCode, stamp, oldCode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CourseContentCounts reports how much content still points at a course number.
// Deleting a course is refused while any of these is non-zero.
func (s *Store) CourseContentCounts(ctx context.Context, code string) (textbooks, resources, banks, submissions int, err error) {
	code = strings.TrimSpace(code)
	queries := []struct {
		query string
		dest  *int
	}{
		{`SELECT COUNT(*) FROM textbook_courses WHERE course_code = ?`, &textbooks},
		{`SELECT COUNT(*) FROM resources WHERE course_code = ?`, &resources},
		{`SELECT COUNT(*) FROM question_bank_courses WHERE course_code = ?`, &banks},
		{`SELECT COUNT(*) FROM resource_submissions WHERE course_code = ?`, &submissions},
	}
	for _, item := range queries {
		if err := s.DB.QueryRowContext(ctx, item.query, code).Scan(item.dest); err != nil {
			return 0, 0, 0, 0, err
		}
	}
	return textbooks, resources, banks, submissions, nil
}

func (s *Store) DeleteCourse(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM courses WHERE id = ?", id)
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

type rowScannerCourse interface {
	Scan(dest ...any) error
}

func scanCourse(row rowScannerCourse) (Course, error) {
	var item Course
	var created, updated int64
	if err := row.Scan(&item.ID, &item.Code, &item.Name, &created, &updated); err != nil {
		return Course{}, err
	}
	item.CreatedAt = fromMillis(created)
	item.UpdatedAt = fromMillis(updated)
	return item, nil
}

// CourseAnalysisStats is the number of PDF papers in a course and how many of
// them have both AI reports.
type CourseAnalysisStats struct {
	Analyzable int64
	Analyzed   int64
}

// AnalysisStatsByCourse returns basic+more completion counts per course.
func (s *Store) AnalysisStatsByCourse(ctx context.Context, codes []string) (map[string]CourseAnalysisStats, error) {
	out := make(map[string]CourseAnalysisStats, len(codes))
	if len(codes) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(codes))
	args := make([]any, 0, len(codes))
	for _, code := range codes {
		placeholders = append(placeholders, "?")
		args = append(args, code)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.course_code,
			SUM(CASE WHEN r.pdf_key <> '' AND LOWER(r.pdf_key) LIKE '%.pdf' AND r.archived_at = 0 THEN 1 ELSE 0 END),
			SUM(CASE WHEN r.pdf_key <> '' AND LOWER(r.pdf_key) LIKE '%.pdf' AND r.archived_at = 0
				AND EXISTS (SELECT 1 FROM resource_ai_tasks a WHERE a.resource_id = r.id AND a.status = 'succeeded'
					AND a.output_key <> '' AND a.more_output_key <> '')
				THEN 1 ELSE 0 END)
		FROM resources r WHERE r.course_code IN (`+strings.Join(placeholders, ",")+`)
		GROUP BY r.course_code`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var stats CourseAnalysisStats
		if err := rows.Scan(&code, &stats.Analyzable, &stats.Analyzed); err != nil {
			return nil, err
		}
		out[code] = stats
	}
	return out, rows.Err()
}
