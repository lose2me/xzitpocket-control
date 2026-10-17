package app

import (
	"context"
	"net/http"
	"strings"
)

// ResourceCourseView is one course entry in the app resource centre.
type ResourceCourseView struct {
	Code              string `json:"code"`
	Name              string `json:"name"`
	QuestionBankCount int    `json:"question_bank_count"`
	ResourceCount     int    `json:"resource_count"`
	TextbookCount     int    `json:"textbook_count"`
}

func (a *App) ListResourceCourses(ctx context.Context) ([]ResourceCourseView, error) {
	items, err := a.Store.ListResourceCourses(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]ResourceCourseView, 0, len(items))
	for _, item := range items {
		views = append(views, ResourceCourseView{
			Code: item.Code, Name: item.Name,
			QuestionBankCount: item.QuestionBankCount,
			ResourceCount:     item.ResourceCount,
			TextbookCount:     item.TextbookCount,
		})
	}
	return views, nil
}

func normalizeCourseCode(code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 || strings.ContainsAny(code, "\r\n\x00") {
		return "", Err("invalid_course_code", "课程编号无效", http.StatusBadRequest)
	}
	return code, nil
}

// ListCourseResourcesForUser returns the resources of one course for a signed-in
// app user. The rows carry metadata only; a signed URL is issued on demand.
func (a *App) ListCourseResourcesForUser(ctx context.Context, courseCode string) ([]ResourceView, int, error) {
	code, err := normalizeCourseCode(courseCode)
	if err != nil {
		return nil, 0, err
	}
	items, err := a.Store.ListResources(ctx, 200, 0, "", code, "", "")
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountResources(ctx, "", code)
	if err != nil {
		return nil, 0, err
	}
	views := make([]ResourceView, 0, len(items))
	for _, item := range items {
		views = append(views, resourceView(item))
	}
	return views, total, nil
}

// ListCourseTextbooksForUser returns the textbooks of one course for a signed-in
// app user. A textbook associated with several courses appears in each.
func (a *App) ListCourseTextbooksForUser(ctx context.Context, courseCode string) ([]TextbookView, int, error) {
	code, err := normalizeCourseCode(courseCode)
	if err != nil {
		return nil, 0, err
	}
	grouped, err := a.Store.ListTextbooksForCourses(ctx, []string{code})
	if err != nil {
		return nil, 0, err
	}
	items := grouped[code]
	views := make([]TextbookView, 0, len(items))
	for _, item := range items {
		views = append(views, textbookView(item))
	}
	return views, len(views), nil
}
