package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
)

// ResourceCourse is one course entry of the app resource centre. All collected courses are returned, including those awaiting resources.
// Version changes whenever the course's app-visible resources or textbooks
// change; the app uses it to decide whether its cached course detail is stale.
type ResourceCourse struct {
	Code              string
	Name              string
	QuestionBankCount int
	ResourceCount     int
	TextbookCount     int
	CoverTextbookID   string
	CoverVersion      string
	Version           string
}

// Revisions are changed in the same transaction as the content. Counts and
// MAX(updated_at) miss edits to an older row and replacements within a tick.
func (s *Store) CourseContentRevision(ctx context.Context, code string) (string, error) {
	var revision int64
	err := s.DB.QueryRowContext(ctx, "SELECT COALESCE((SELECT revision FROM resource_revisions WHERE scope = ?), 0)", code).Scan(&revision)
	return fmt.Sprint(revision), err
}

func (s *Store) ResourceCenterRevision(ctx context.Context) (string, error) {
	return s.CourseContentRevision(ctx, "")
}

// initResourceRevisionTriggers keeps cache invalidation independent of which
// service writes the content, including cascaded association changes.
func initResourceRevisionTriggers(ctx context.Context, tx *sql.Tx) error {
	for _, spec := range []struct{ table, columns, courses, condition string }{
		{"courses", "code,name", "SELECT %s.code AS code", ""},
		{"resources", "name,tags,course_code,pdf_key,manual_ai_key,manual_ai_size,manual_ai_updated_at,archived_at", "SELECT %s.course_code AS code", ""},
		{"textbooks", "name,tags,course_code,pdf_key,cover_key,updated_at,archived_at", "SELECT course_code AS code FROM textbook_courses WHERE textbook_id = %s.id", ""},
		{"textbook_courses", "textbook_id,course_code", "SELECT %s.course_code AS code", ""},
		{"question_banks", "name,status,requires_cdk,course_code,updated_at,archived_at", "SELECT course_code AS code FROM question_bank_courses WHERE question_bank_id = %s.id", ""},
		{"question_bank_courses", "question_bank_id,course_code", "SELECT %s.course_code AS code", ""},
		{"resource_ai_tasks", "status,solvable,total_pages,output_key,output_size,more_output_key,more_output_size,markdown,more_markdown", "SELECT course_code AS code FROM resources WHERE id = %s.resource_id", "%s.status = 'succeeded'"},
	} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			refs := []string{"NEW"}
			if event == "DELETE" {
				refs = []string{"OLD"}
			}
			if event == "UPDATE" {
				refs = []string{"OLD", "NEW"}
			}
			selects, conditions := make([]string, 0, len(refs)), make([]string, 0, len(refs))
			for _, ref := range refs {
				selects = append(selects, fmt.Sprintf(spec.courses, ref))
				if spec.condition != "" {
					conditions = append(conditions, fmt.Sprintf(spec.condition, ref))
				}
			}
			predicates := []string{}
			if len(conditions) > 0 {
				predicates = append(predicates, "("+strings.Join(conditions, " OR ")+")")
			}
			action := event
			if event == "UPDATE" {
				action += " OF " + spec.columns
				changes := []string{}
				for _, column := range strings.Split(spec.columns, ",") {
					changes = append(changes, "OLD."+column+" IS NOT NEW."+column)
				}
				predicates = append(predicates, "("+strings.Join(changes, " OR ")+")")
			}
			when := ""
			if len(predicates) > 0 {
				when = " WHEN " + strings.Join(predicates, " AND ")
			}
			statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS revision_%s_%s AFTER %s ON %s%s BEGIN
    UPDATE resource_revisions SET revision = revision + 1 WHERE scope = '';
    INSERT INTO resource_revisions(scope, revision)
     SELECT code, (SELECT revision FROM resource_revisions WHERE scope = '')
     FROM (%s) WHERE code <> '' GROUP BY code
     ON CONFLICT(scope) DO UPDATE SET revision = excluded.revision;
   END`, spec.table, event, action, spec.table, when, strings.Join(selects, " UNION "))
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("create resource revision trigger: %w", err)
			}
		}
	}
	return nil
}

func (s *Store) ListResourceCourses(ctx context.Context) ([]ResourceCourse, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.code, c.name,
			(SELECT COUNT(*) FROM question_bank_courses qbc
				JOIN question_banks b ON b.id = qbc.question_bank_id
				WHERE qbc.course_code = c.code AND b.status = 'active' AND b.archived_at = 0),
			(SELECT COUNT(*) FROM resources r WHERE r.course_code = c.code AND r.pdf_key <> '' AND r.archived_at = 0),
			(SELECT COUNT(*) FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.pdf_key <> '' AND t.archived_at = 0),
			COALESCE((SELECT tc.textbook_id FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.archived_at = 0 AND t.cover_key <> ''
				ORDER BY t.name, t.id LIMIT 1), ''),
			COALESCE((SELECT t.cover_key || ':' || t.updated_at FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.archived_at = 0 AND t.cover_key <> ''
				ORDER BY t.name, t.id LIMIT 1), ''),
			COALESCE((SELECT revision FROM resource_revisions WHERE scope = c.code), 0)
		FROM courses c
		WHERE c.code <> ''
		ORDER BY c.name ASC, c.code ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ResourceCourse, 0)
	for rows.Next() {
		var item ResourceCourse
		var coverKey string
		var revision int64
		if err := rows.Scan(&item.Code, &item.Name, &item.QuestionBankCount, &item.ResourceCount, &item.TextbookCount, &item.CoverTextbookID, &coverKey, &revision); err != nil {
			return nil, err
		}
		if coverKey != "" {
			sum := sha256.Sum256([]byte(coverKey))
			item.CoverVersion = fmt.Sprintf("%x", sum[:8])
		}
		item.Version = fmt.Sprint(revision)
		items = append(items, item)
	}
	return items, rows.Err()
}
