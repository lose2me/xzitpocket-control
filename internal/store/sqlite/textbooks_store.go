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
// for sorting and older clients; CourseCodes holds every associated course.
type Textbook struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Tags          string    `json:"tags"`
	CourseCode    string    `json:"course_code"`
	CourseCodes   []string  `json:"course_codes"`
	PdfKey        string    `json:"pdf_key"`
	CoverKey      string    `json:"cover_key"`
	DownloadCount int64     `json:"download_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type TextbookInput struct {
	Name        string
	Tags        string
	CourseCode  string
	CourseCodes []string
}

// textbookInputCodes merges the legacy single course with the multi-course
// list, dropping blanks and duplicates while keeping the caller's order.
func textbookInputCodes(input TextbookInput) []string {
	codes := make([]string, 0, len(input.CourseCodes)+1)
	seen := make(map[string]bool, len(input.CourseCodes)+1)
	add := func(code string) {
		code = strings.TrimSpace(code)
		if code == "" || seen[code] {
			return
		}
		seen[code] = true
		codes = append(codes, code)
	}
	for _, code := range input.CourseCodes {
		add(code)
	}
	add(input.CourseCode)
	return codes
}

const textbookColumns = "id, name, tags, course_code, pdf_key, cover_key, download_count, created_at, updated_at"

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
		codes := textbookInputCodes(input)
		primary := ""
		if len(codes) > 0 {
			primary = codes[0]
		}
		id := newStoreIDPrefix("tbx")
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

func textbookWhere(search string) (string, []any) {
	if search = strings.TrimSpace(search); search == "" {
		return "", nil
	}
	like := "%" + escapeLike(search) + "%"
	return " WHERE (t.name LIKE ? ESCAPE '\\' OR t.tags LIKE ? ESCAPE '\\' OR EXISTS (SELECT 1 FROM textbook_courses tc WHERE tc.textbook_id = t.id AND tc.course_code LIKE ? ESCAPE '\\'))",
		[]any{like, like, like}
}

func (s *Store) ListTextbooks(ctx context.Context, limit, offset int, search, sort, order string) ([]Textbook, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	where, args := textbookWhere(search)
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

func (s *Store) CountTextbooks(ctx context.Context, search string) (int, error) {
	where, args := textbookWhere(search)
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
	rows, err := s.DB.QueryContext(ctx, `SELECT tc.course_code, t.id, t.name, t.tags, t.course_code, t.pdf_key, t.cover_key, t.download_count, t.created_at, t.updated_at
		FROM textbook_courses tc JOIN textbooks t ON t.id = tc.textbook_id
		WHERE tc.course_code IN (`+strings.Join(placeholders, ",")+") ORDER BY t.name ASC, t.id ASC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var item Textbook
		var created, updated int64
		if err := rows.Scan(&code, &item.ID, &item.Name, &item.Tags, &item.CourseCode, &item.PdfKey, &item.CoverKey, &item.DownloadCount, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = fromMillis(created), fromMillis(updated)
		item.CourseCodes = []string{code}
		grouped[code] = append(grouped[code], item)
	}
	return grouped, rows.Err()
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
	codes := textbookInputCodes(input)
	primary := ""
	if len(codes) > 0 {
		primary = codes[0]
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Textbook{}, err
	}
	defer func() { _ = tx.Rollback() }()
	id := newStoreIDPrefix("tbx")
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
	codes := textbookInputCodes(input)
	primary := ""
	if len(codes) > 0 {
		primary = codes[0]
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE textbooks SET name = ?, tags = ?, course_code = ?, updated_at = ? WHERE id = ?`,
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
// Rows written before multi-course support fall back to the legacy column.
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
		if list := codes[items[i].ID]; len(list) > 0 {
			items[i].CourseCodes = list
		} else if items[i].CourseCode != "" {
			items[i].CourseCodes = []string{items[i].CourseCode}
		}
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
	var created, updated int64
	if err := row.Scan(&item.ID, &item.Name, &item.Tags, &item.CourseCode, &item.PdfKey, &item.CoverKey, &item.DownloadCount, &created, &updated); err != nil {
		return Textbook{}, err
	}
	item.CreatedAt = fromMillis(created)
	item.UpdatedAt = fromMillis(updated)
	return item, nil
}

// SetTextbookPdfKey records the COS key of the textbook's PDF.
func (s *Store) SetTextbookPdfKey(ctx context.Context, id, key string, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE textbooks SET pdf_key = ?, updated_at = ? WHERE id = ?`, key, millis(now), id)
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

// SetTextbookCoverKey records the COS key of the textbook's WebP cover.
func (s *Store) SetTextbookCoverKey(ctx context.Context, id, key string, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE textbooks SET cover_key = ?, updated_at = ? WHERE id = ?`, key, millis(now), id)
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
