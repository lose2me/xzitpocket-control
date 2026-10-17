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

// Textbook is one book a course uses. The course association is by course code
// and is edited manually in the console, so changing the book fields keeps it.
type Textbook struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Tags       string    `json:"tags"`
	CourseCode string    `json:"course_code"`
	PdfKey     string    `json:"pdf_key"`
	CoverKey   string    `json:"cover_key"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type TextbookInput struct {
	Name       string
	Tags       string
	CourseCode string
}

const textbookColumns = "id, name, tags, course_code, pdf_key, cover_key, created_at, updated_at"

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
		result, err := tx.ExecContext(ctx, `INSERT INTO textbooks(id, name, tags, course_code, pdf_key, cover_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', '', ?, ?)`,
			newStoreIDPrefix("tbx"), input.Name, input.Tags, input.CourseCode, millis(now), millis(now))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return 0, err
		}
		if affected, countErr := result.RowsAffected(); countErr == nil && affected == 1 {
			accepted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return accepted, nil
}

var textbookListSorts = map[string]string{
	"name":        "t.name",
	"tags":        "t.tags",
	"course_code": "t.course_code",
	"created_at":  "t.created_at",
	"updated_at":  "t.updated_at",
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
	return " WHERE (t.name LIKE ? ESCAPE '\\' OR t.tags LIKE ? ESCAPE '\\' OR t.course_code LIKE ? ESCAPE '\\')",
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
	return result, rows.Err()
}

func (s *Store) CountTextbooks(ctx context.Context, search string) (int, error) {
	where, args := textbookWhere(search)
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM textbooks t"+where, args...).Scan(&count)
	return count, err
}

// ListTextbooksForCourses groups the textbooks of the given course codes.
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
	rows, err := s.DB.QueryContext(ctx, "SELECT "+textbookColumns+" FROM textbooks WHERE course_code IN ("+
		strings.Join(placeholders, ",")+") ORDER BY name ASC, id ASC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanTextbook(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		grouped[item.CourseCode] = append(grouped[item.CourseCode], item)
	}
	return grouped, rows.Err()
}

func (s *Store) GetTextbook(ctx context.Context, id string) (Textbook, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT "+textbookColumns+" FROM textbooks WHERE id = ?", id)
	item, err := scanTextbook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Textbook{}, sql.ErrNoRows
	}
	return item, err
}

func (s *Store) CreateTextbook(ctx context.Context, input TextbookInput, now time.Time) (Textbook, error) {
	id := newStoreIDPrefix("tbx")
	result, err := s.DB.ExecContext(ctx, `INSERT INTO textbooks(id, name, tags, course_code, pdf_key, cover_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', '', ?, ?)`,
		id, input.Name, input.Tags, input.CourseCode, millis(now), millis(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Textbook{}, ErrTextbookExists
		}
		return Textbook{}, err
	}
	if affected, countErr := result.RowsAffected(); countErr == nil && affected != 1 {
		return Textbook{}, ErrTextbookExists
	}
	return s.GetTextbook(ctx, id)
}

func (s *Store) UpdateTextbook(ctx context.Context, id string, input TextbookInput, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE textbooks SET name = ?, tags = ?, course_code = ?, updated_at = ? WHERE id = ?`,
		input.Name, input.Tags, input.CourseCode, millis(now), id)
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
	if err := row.Scan(&item.ID, &item.Name, &item.Tags, &item.CourseCode, &item.PdfKey, &item.CoverKey, &created, &updated); err != nil {
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
