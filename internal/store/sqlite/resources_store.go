package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrResourceExists is returned when an administrator creates or renames a
// resource onto a (name, tags, course) key that is already taken.
var ErrResourceExists = errors.New("resource already exists")

// Resource is one file an administrator attaches to a course. The course
// association is by course code and is edited manually in the console, so
// changing the resource fields keeps it.
type Resource struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Tags              string    `json:"tags"`
	CourseCode        string    `json:"course_code"`
	PdfKey            string    `json:"pdf_key"`
	DownloadCount     int64     `json:"download_count"`
	ContributorUserID string    `json:"contributor_user_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ResourceInput struct {
	Name       string
	Tags       string
	CourseCode string
}

const resourceColumns = "id, name, tags, course_code, pdf_key, download_count, contributor_user_id, created_at, updated_at"

var resourceListSorts = map[string]string{
	"name":           "r.name",
	"tags":           "r.tags",
	"course_code":    "r.course_code",
	"download_count": "r.download_count",
	"created_at":     "r.created_at",
	"updated_at":     "r.updated_at",
}

const resourceDefaultOrderBy = "r.course_code ASC, r.name ASC, r.id"

func resourceOrderBy(sort, order string) string {
	column, ok := resourceListSorts[strings.TrimSpace(sort)]
	if !ok {
		return resourceDefaultOrderBy
	}
	direction := "ASC"
	if strings.EqualFold(strings.TrimSpace(order), "desc") {
		direction = "DESC"
	}
	return column + " " + direction + ", r.id"
}

func resourceWhere(search, courseCode string) (string, []any) {
	clauses := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if search = strings.TrimSpace(search); search != "" {
		like := "%" + escapeLike(search) + "%"
		clauses = append(clauses, "(r.name LIKE ? ESCAPE '\\' OR r.tags LIKE ? ESCAPE '\\' OR r.course_code LIKE ? ESCAPE '\\')")
		args = append(args, like, like, like)
	}
	if courseCode = strings.TrimSpace(courseCode); courseCode != "" {
		clauses = append(clauses, "r.course_code = ?")
		args = append(args, courseCode)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (s *Store) ListResources(ctx context.Context, limit, offset int, search, courseCode, sort, order string) ([]Resource, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	where, args := resourceWhere(search, courseCode)
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+resourceColumns+" FROM resources r"+where+
		" ORDER BY "+resourceOrderBy(sort, order)+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Resource, 0)
	for rows.Next() {
		item, scanErr := scanResource(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CountResources(ctx context.Context, search, courseCode string) (int, error) {
	where, args := resourceWhere(search, courseCode)
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM resources r"+where, args...).Scan(&count)
	return count, err
}

// CountResourcesByCourse returns the resource count for each of the given
// course codes. Codes without resources are omitted.
func (s *Store) CountResourcesByCourse(ctx context.Context, codes []string) (map[string]int, error) {
	counts := make(map[string]int, len(codes))
	if len(codes) == 0 {
		return counts, nil
	}
	placeholders := make([]string, 0, len(codes))
	args := make([]any, 0, len(codes))
	for _, code := range codes {
		placeholders = append(placeholders, "?")
		args = append(args, code)
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT course_code, COUNT(*) FROM resources WHERE course_code IN ("+
		strings.Join(placeholders, ",")+") GROUP BY course_code", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var count int
		if err := rows.Scan(&code, &count); err != nil {
			return nil, err
		}
		counts[code] = count
	}
	return counts, rows.Err()
}

func (s *Store) GetResource(ctx context.Context, id string) (Resource, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT "+resourceColumns+" FROM resources WHERE id = ?", id)
	item, err := scanResource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, sql.ErrNoRows
	}
	return item, err
}

func (s *Store) CreateResource(ctx context.Context, input ResourceInput, now time.Time) (Resource, error) {
	id := newStoreIDPrefix("res")
	result, err := s.DB.ExecContext(ctx, `INSERT INTO resources(id, name, tags, course_code, pdf_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, ?)`,
		id, input.Name, input.Tags, input.CourseCode, millis(now), millis(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Resource{}, ErrResourceExists
		}
		return Resource{}, err
	}
	if affected, countErr := result.RowsAffected(); countErr == nil && affected != 1 {
		return Resource{}, ErrResourceExists
	}
	return s.GetResource(ctx, id)
}

func (s *Store) DeleteResource(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM resources WHERE id = ?", id)
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

type rowScannerResource interface {
	Scan(dest ...any) error
}

func scanResource(row rowScannerResource) (Resource, error) {
	var item Resource
	var created, updated int64
	if err := row.Scan(&item.ID, &item.Name, &item.Tags, &item.CourseCode, &item.PdfKey, &item.DownloadCount, &item.ContributorUserID, &created, &updated); err != nil {
		return Resource{}, err
	}
	item.CreatedAt = fromMillis(created)
	item.UpdatedAt = fromMillis(updated)
	return item, nil
}

// SetResourceContributor links an approved upload to the student who
// contributed it. Downloads of this exam count toward that user's stats.
func (s *Store) SetResourceContributor(ctx context.Context, id, userID string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE resources SET contributor_user_id = ? WHERE id = ?", userID, id)
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

// IncrementResourceDownloads counts one PDF download without touching the
// content's updated_at time.
func (s *Store) IncrementResourceDownloads(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE resources SET download_count = download_count + 1 WHERE id = ?", id)
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

// SetResourcePdfKey records the COS key of the resource's PDF.
func (s *Store) SetResourcePdfKey(ctx context.Context, id, key string, now time.Time) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE resources SET pdf_key = ?, updated_at = ? WHERE id = ?`, key, millis(now), id)
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
