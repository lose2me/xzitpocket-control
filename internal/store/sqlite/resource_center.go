package sqlite

import (
	"context"
	"fmt"
)

// ResourceCourse is one course entry of the app resource centre. Only courses
// that actually have a published bank, a resource PDF or a textbook are
// returned, so the client never lists empty courses.
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

// contentVersion combines the row count and newest timestamp of each content
// type. A count catches additions and deletions (which do not change the
// newest timestamp), while the timestamp catches edits.
func contentVersion(resourceCount, resourceUpdated, textbookCount, textbookUpdated, courseUpdated int64) string {
	return fmt.Sprintf("r%d.%d:t%d.%d:c%d", resourceCount, resourceUpdated, textbookCount, textbookUpdated, courseUpdated)
}

// CourseContentRevision returns the version of one course's app-visible
// resources and textbooks.
func (s *Store) CourseContentRevision(ctx context.Context, code string) (string, error) {
	var resourceCount, resourceUpdated, textbookCount, textbookUpdated, courseUpdated int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM resources r WHERE r.course_code = ? AND r.archived_at = 0),
			(SELECT COALESCE(MAX(r.updated_at), 0) FROM resources r WHERE r.course_code = ? AND r.archived_at = 0),
			(SELECT COUNT(*) FROM textbook_courses tc JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = ? AND t.archived_at = 0),
			(SELECT COALESCE(MAX(t.updated_at), 0) FROM textbook_courses tc JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = ? AND t.archived_at = 0),
			(SELECT COALESCE(MAX(c.updated_at), 0) FROM courses c WHERE c.code = ?)`, code, code, code, code, code).
		Scan(&resourceCount, &resourceUpdated, &textbookCount, &textbookUpdated, &courseUpdated)
	if err != nil {
		return "", err
	}
	return contentVersion(resourceCount, resourceUpdated, textbookCount, textbookUpdated, courseUpdated), nil
}

// ResourceCenterRevision returns a value that changes whenever any content
// shown by the app's resource centre is added, removed, archived or edited.
func (s *Store) ResourceCenterRevision(ctx context.Context) (string, error) {
	var courses, courseUpdated int64
	var resources, resourceUpdated int64
	var textbooks, textbookUpdated int64
	var banks, bankUpdated int64
	var textbookLinks, bankLinks int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM courses WHERE code <> ''),
			(SELECT COALESCE(MAX(updated_at), 0) FROM courses),
			(SELECT COUNT(*) FROM resources WHERE archived_at = 0),
			(SELECT COALESCE(MAX(updated_at), 0) FROM resources WHERE archived_at = 0),
			(SELECT COUNT(*) FROM textbooks WHERE archived_at = 0),
			(SELECT COALESCE(MAX(updated_at), 0) FROM textbooks WHERE archived_at = 0),
			(SELECT COUNT(*) FROM question_banks WHERE status = 'active' AND archived_at = 0),
			(SELECT COALESCE(MAX(updated_at), 0) FROM question_banks WHERE status = 'active' AND archived_at = 0),
			(SELECT COUNT(*) FROM textbook_courses),
			(SELECT COUNT(*) FROM question_bank_courses)`).
		Scan(&courses, &courseUpdated, &resources, &resourceUpdated, &textbooks, &textbookUpdated, &banks, &bankUpdated, &textbookLinks, &bankLinks)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("c%d.%d:r%d.%d:t%d.%d:q%d.%d:tc%d:qc%d",
		courses, courseUpdated, resources, resourceUpdated, textbooks, textbookUpdated,
		banks, bankUpdated, textbookLinks, bankLinks), nil
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
				WHERE tc.course_code = c.code AND t.archived_at = 0
				ORDER BY t.name, t.id LIMIT 1), ''),
			COALESCE((SELECT t.updated_at FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.archived_at = 0
				ORDER BY t.name, t.id LIMIT 1), 0),
			(SELECT COUNT(*) FROM resources r WHERE r.course_code = c.code AND r.archived_at = 0),
			(SELECT COALESCE(MAX(r.updated_at), 0) FROM resources r WHERE r.course_code = c.code AND r.archived_at = 0),
			(SELECT COUNT(*) FROM textbook_courses tc JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.archived_at = 0),
			(SELECT COALESCE(MAX(t.updated_at), 0) FROM textbook_courses tc JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.archived_at = 0),
			c.updated_at
		FROM courses c
		WHERE c.code <> '' AND (
			EXISTS (SELECT 1 FROM question_bank_courses qbc
				JOIN question_banks b ON b.id = qbc.question_bank_id
				WHERE qbc.course_code = c.code AND b.status = 'active' AND b.archived_at = 0)
			OR EXISTS (SELECT 1 FROM resources r WHERE r.course_code = c.code AND r.pdf_key <> '' AND r.archived_at = 0)
			OR EXISTS (SELECT 1 FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.pdf_key <> '' AND t.archived_at = 0)
		)
		ORDER BY c.name ASC, c.code ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ResourceCourse, 0)
	for rows.Next() {
		var item ResourceCourse
		var coverUpdated, resourceCount, resourceUpdated, textbookCount, textbookUpdated, courseUpdated int64
		if err := rows.Scan(&item.Code, &item.Name, &item.QuestionBankCount, &item.ResourceCount, &item.TextbookCount, &item.CoverTextbookID,
			&coverUpdated, &resourceCount, &resourceUpdated, &textbookCount, &textbookUpdated, &courseUpdated); err != nil {
			return nil, err
		}
		if coverUpdated > 0 {
			item.CoverVersion = fmt.Sprintf("%d", coverUpdated)
		}
		item.Version = contentVersion(resourceCount, resourceUpdated, textbookCount, textbookUpdated, courseUpdated)
		items = append(items, item)
	}
	return items, rows.Err()
}
