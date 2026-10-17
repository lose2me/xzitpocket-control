package sqlite

import "context"

// ResourceCourse is one course entry of the app resource centre. Only courses
// that actually have a published bank, a resource PDF or a textbook are
// returned, so the client never lists empty courses.
type ResourceCourse struct {
	Code              string
	Name              string
	QuestionBankCount int
	ResourceCount     int
	TextbookCount     int
	CoverTextbookID   string
}

func (s *Store) ListResourceCourses(ctx context.Context) ([]ResourceCourse, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.code, c.name,
			(SELECT COUNT(*) FROM question_bank_courses qbc
				JOIN question_banks b ON b.id = qbc.question_bank_id
				WHERE qbc.course_code = c.code AND b.status = 'active'),
			(SELECT COUNT(*) FROM resources r WHERE r.course_code = c.code AND r.pdf_key <> ''),
			(SELECT COUNT(*) FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.pdf_key <> ''),
			COALESCE((SELECT tc.textbook_id FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code
				ORDER BY t.name, t.id LIMIT 1), '')
		FROM courses c
		WHERE c.code <> '' AND (
			EXISTS (SELECT 1 FROM question_bank_courses qbc
				JOIN question_banks b ON b.id = qbc.question_bank_id
				WHERE qbc.course_code = c.code AND b.status = 'active')
			OR EXISTS (SELECT 1 FROM resources r WHERE r.course_code = c.code AND r.pdf_key <> '')
			OR EXISTS (SELECT 1 FROM textbook_courses tc
				JOIN textbooks t ON t.id = tc.textbook_id
				WHERE tc.course_code = c.code AND t.pdf_key <> '')
		)
		ORDER BY c.name ASC, c.code ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ResourceCourse, 0)
	for rows.Next() {
		var item ResourceCourse
		if err := rows.Scan(&item.Code, &item.Name, &item.QuestionBankCount, &item.ResourceCount, &item.TextbookCount, &item.CoverTextbookID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
