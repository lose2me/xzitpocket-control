package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrTextbookExists is returned when an administrator creates or renames a
// textbook onto a (name, tags, course) key that is already taken.
var ErrTextbookExists = errors.New("textbook already exists")

// Textbook is one book a course uses. CourseCode keeps the first association
// for sorting and uniqueness; CourseCodes holds every associated course.
type Textbook struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Tags              string    `json:"tags"`
	CourseCode        string    `json:"course_code"`
	CourseCodes       []string  `json:"course_codes"`
	PdfKey            string    `json:"pdf_key"`
	CoverKey          string    `json:"cover_key"`
	DownloadCount     int64     `json:"download_count"`
	ContributorUserID string    `json:"contributor_user_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ArchivedAt        time.Time `json:"archived_at"`
}

type TextbookInput struct {
	Name        string
	Tags        string
	CourseCodes []string
}

// normalizeCourseCodes drops blanks and duplicates while keeping the caller's order.
func normalizeCourseCodes(input []string) []string {
	codes := make([]string, 0, len(input))
	seen := make(map[string]bool, len(input))
	add := func(code string) {
		code = strings.TrimSpace(code)
		if code == "" || seen[code] {
			return
		}
		seen[code] = true
		codes = append(codes, code)
	}
	for _, code := range input {
		add(code)
	}
	return codes
}

const textbookColumns = "id, name, tags, course_code, pdf_key, cover_key, download_count, contributor_user_id, created_at, updated_at, archived_at"

// UploadTextbooks inserts every unseen textbook. Existing rows are never
// modified, so a stored course association survives later uploads.
func (s *Store) UploadTextbooks(ctx context.Context, inputs []TextbookInput, now time.Time) (int, error) {
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
		if strings.TrimSpace(input.Name) == "" {
			continue
		}
		codes := normalizeCourseCodes(input.CourseCodes)
		primary := ""
		if len(codes) > 0 {
			primary = codes[0]
		}
		id, err := newStoreID("tbx")
		if err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO textbooks(id, name, tags, course_code, pdf_key, cover_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', '', ?, ?)`,
			id, input.Name, input.Tags, primary, millis(now), millis(now))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return 0, err
		}
		if affected, countErr := result.RowsAffected(); countErr == nil && affected == 1 {
			if err := insertTextbookCourses(ctx, tx, id, codes); err != nil {
				return 0, err
			}
			accepted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return accepted, nil
}

var textbookListSorts = map[string]string{
	"name":           "t.name",
	"tags":           "t.tags",
	"course_code":    "t.course_code",
	"download_count": "t.download_count",
	"created_at":     "t.created_at",
	"updated_at":     "t.updated_at",
}

const textbookDefaultOrderBy = "t.course_code ASC, t.name ASC, t.id"

func textbookOrderBy(sort, order string) string {
	column, ok := textbookListSorts[strings.TrimSpace(sort)]
	if !ok {
		return textbookDefaultOrderBy
	}
	direction := "ASC"
	if strings.EqualFold(strings.TrimSpace(order), "desc") {
		direction = "DESC"
	}
	return column + " " + direction + ", t.id"
}

func textbookWhere(search string, archived bool) (string, []any) {
	clauses := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if archived {
		clauses = append(clauses, "t.archived_at > 0")
	} else {
		clauses = append(clauses, "t.archived_at = 0")
	}
	if search = strings.TrimSpace(search); search != "" {
		like := "%" + escapeLike(search) + "%"
		clauses = append(clauses, "(t.name LIKE ? ESCAPE '\\' OR t.tags LIKE ? ESCAPE '\\' OR EXISTS (SELECT 1 FROM textbook_courses tc WHERE tc.textbook_id = t.id AND tc.course_code LIKE ? ESCAPE '\\'))")
		args = append(args, like, like, like)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (s *Store) ListTextbooks(ctx context.Context, limit, offset int, search, sort, order string, archived bool) ([]Textbook, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	where, args := textbookWhere(search, archived)
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+textbookColumns+" FROM textbooks t"+where+
		" ORDER BY "+textbookOrderBy(sort, order)+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Textbook, 0)
	for rows.Next() {
		item, scanErr := scanTextbook(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := s.attachTextbookCourses(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) CountTextbooks(ctx context.Context, search string, archived bool) (int, error) {
	where, args := textbookWhere(search, archived)
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM textbooks t"+where, args...).Scan(&count)
	return count, err
}

// ListTextbooksForCourses groups the textbooks of the given course codes. A
// book associated with several courses is included under each of them.
func (s *Store) ListTextbooksForCourses(ctx context.Context, codes []string) (map[string][]Textbook, error) {
	grouped := make(map[string][]Textbook, len(codes))
	if len(codes) == 0 {
		return grouped, nil
	}
	placeholders := make([]string, 0, len(codes))
	args := make([]any, 0, len(codes))
	for _, code := range codes {
		placeholders = append(placeholders, "?")
		args = append(args, code)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT tc.course_code, t.id, t.name, t.tags, t.course_code, t.pdf_key, t.cover_key, t.download_count, t.contributor_user_id, t.created_at, t.updated_at, t.archived_at
		FROM textbook_courses tc JOIN textbooks t ON t.id = tc.textbook_id
		WHERE tc.course_code IN (`+strings.Join(placeholders, ",")+") AND t.archived_at = 0 ORDER BY t.name ASC, t.id ASC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var item Textbook
		var created, updated, archived int64
		if err := rows.Scan(&code, &item.ID, &item.Name, &item.Tags, &item.CourseCode, &item.PdfKey, &item.CoverKey, &item.DownloadCount, &item.ContributorUserID, &created, &updated, &archived); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = fromMillis(created), fromMillis(updated)
		item.ArchivedAt = optionalFromMillis(archived)
		item.CourseCodes = []string{code}
		grouped[code] = append(grouped[code], item)
	}
	return grouped, rows.Err()
}

// ListAllTextbooks returns every textbook, used by the app upload picker.
func (s *Store) ListAllTextbooks(ctx context.Context) ([]Textbook, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+textbookColumns+" FROM textbooks t WHERE t.archived_at = 0 ORDER BY t.name ASC, t.id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Textbook, 0)
	for rows.Next() {
		item, scanErr := scanTextbook(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachTextbookCourses(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) GetTextbook(ctx context.Context, id string) (Textbook, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT "+textbookColumns+" FROM textbooks WHERE id = ?", id)
	item, err := scanTextbook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Textbook{}, sql.ErrNoRows
	}
	if err != nil {
		return Textbook{}, err
	}
	items := []Textbook{item}
	if err := s.attachTextbookCourses(ctx, items); err != nil {
		return Textbook{}, err
	}
	return items[0], nil
}

func (s *Store) CreateTextbook(ctx context.Context, input TextbookInput, now time.Time) (Textbook, error) {
	codes := normalizeCourseCodes(input.CourseCodes)
	primary := ""
	if len(codes) > 0 {
		primary = codes[0]
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Textbook{}, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := newStoreID("tbx")
	if err != nil {
		return Textbook{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO textbooks(id, name, tags, course_code, pdf_key, cover_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', '', ?, ?)`,
		id, input.Name, input.Tags, primary, millis(now), millis(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Textbook{}, ErrTextbookExists
		}
		return Textbook{}, err
	}
	if affected, countErr := result.RowsAffected(); countErr == nil && affected != 1 {
		return Textbook{}, ErrTextbookExists
	}
	if err := insertTextbookCourses(ctx, tx, id, codes); err != nil {
		return Textbook{}, err
	}
	if err := tx.Commit(); err != nil {
		return Textbook{}, err
	}
	return s.GetTextbook(ctx, id)
}

func (s *Store) UpdateTextbook(ctx context.Context, id string, input TextbookInput, now time.Time) error {
	codes := normalizeCourseCodes(input.CourseCodes)
	primary := ""
	if len(codes) > 0 {
		primary = codes[0]
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE textbooks SET name = ?, tags = ?, course_code = ?, updated_at = MAX(updated_at + 1, ?) WHERE id = ?`,
		input.Name, input.Tags, primary, millis(now), id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrTextbookExists
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
	if _, err := tx.ExecContext(ctx, "DELETE FROM textbook_courses WHERE textbook_id = ?", id); err != nil {
		return err
	}
	if err := insertTextbookCourses(ctx, tx, id, codes); err != nil {
		return err
	}
	return tx.Commit()
}

func insertTextbookCourses(ctx context.Context, tx *sql.Tx, id string, codes []string) error {
	for _, code := range codes {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO textbook_courses(textbook_id, course_code) VALUES (?, ?)", id, code); err != nil {
			return err
		}
	}
	return nil
}

// attachTextbookCourses fills CourseCodes for a page of textbooks in one query.
func (s *Store) attachTextbookCourses(ctx context.Context, items []Textbook) error {
	if len(items) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(items))
	args := make([]any, 0, len(items))
	for _, item := range items {
		placeholders = append(placeholders, "?")
		args = append(args, item.ID)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.textbook_id, c.course_code
		FROM textbook_courses c JOIN textbooks t ON t.id = c.textbook_id
		WHERE c.textbook_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY c.textbook_id, CASE WHEN c.course_code = t.course_code THEN 0 ELSE 1 END, c.course_code`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	codes := make(map[string][]string, len(items))
	for rows.Next() {
		var id, code string
		if err := rows.Scan(&id, &code); err != nil {
			return err
		}
		codes[id] = append(codes[id], code)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range items {
		items[i].CourseCodes = codes[items[i].ID]
	}
	return nil
}

func (s *Store) DeleteTextbook(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM textbooks WHERE id = ?", id)
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

type rowScannerTextbook interface {
	Scan(dest ...any) error
}

func scanTextbook(row rowScannerTextbook) (Textbook, error) {
	var item Textbook
	var created, updated, archived int64
	if err := row.Scan(&item.ID, &item.Name, &item.Tags, &item.CourseCode, &item.PdfKey, &item.CoverKey, &item.DownloadCount, &item.ContributorUserID, &created, &updated, &archived); err != nil {
		return Textbook{}, err
	}
	item.CreatedAt = fromMillis(created)
	item.UpdatedAt = fromMillis(updated)
	item.ArchivedAt = optionalFromMillis(archived)
	return item, nil
}

// SetTextbookPdfKey records the COS key of the textbook's PDF.
func (s *Store) SetTextbookPdfKey(ctx context.Context, id, key string, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE textbooks SET pdf_key = ?, updated_at = MAX(updated_at + 1, ?) WHERE id = ?`, key, millis(now), id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// IncrementTextbookDownloads counts one PDF download without touching the
// content's updated_at time.
func (s *Store) IncrementTextbookDownloads(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE textbooks SET download_count = download_count + 1 WHERE id = ?", id)
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

// SetTextbookContributor links an approved upload to the student who
// contributed it. Downloads of this textbook count toward that user's stats.
func (s *Store) SetTextbookContributor(ctx context.Context, id, userID string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE textbooks SET contributor_user_id = ? WHERE id = ?", userID, id)
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

// SetTextbookArchive toggles a textbook between the live list and the archive.
// Object keys are updated together with the flag so a row never points at the
// other area's namespace. Archiving keeps contributor and download counters.
func (s *Store) SetTextbookArchive(ctx context.Context, id, pdfKey, coverKey string, archivedAt time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE textbooks SET pdf_key = ?, cover_key = ?, archived_at = ? WHERE id = ?`,
		pdfKey, coverKey, optionalMillis(archivedAt), id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// SetTextbookCoverKey records a cover and invalidates every textbook sharing
// its object key. A browser can upload new image bytes to the same PDF-based key.
func (s *Store) SetTextbookCoverKey(ctx context.Context, id, key string, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE textbooks SET cover_key = ?, updated_at = MAX(updated_at + 1, ?)
		WHERE (id = ? OR (cover_key = ? AND cover_key <> ''))
		AND EXISTS (SELECT 1 FROM textbooks WHERE id = ?)`, key, millis(now), id, key, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
