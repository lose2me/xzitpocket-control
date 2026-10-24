package app

import (
	"context"
	"net/http"
	"strings"

	"xzitpocket-control/internal/store/sqlite"
)

// ResourceCourseView is one course entry in the app resource centre.
type ResourceCourseView struct {
	Code              string `json:"code"`
	Name              string `json:"name"`
	QuestionBankCount int    `json:"question_bank_count"`
	ResourceCount     int    `json:"resource_count"`
	TextbookCount     int    `json:"textbook_count"`
	CoverURL          string `json:"cover_url,omitempty"`
	CoverVersion      string `json:"cover_version,omitempty"`
	Version           string `json:"version"`
}

func (a *App) ListResourceCourses(ctx context.Context) ([]ResourceCourseView, error) {
	items, err := a.Store.ListResourceCourses(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]ResourceCourseView, 0, len(items))
	for _, item := range items {
		view := ResourceCourseView{
			Code: item.Code, Name: item.Name,
			QuestionBankCount: item.QuestionBankCount,
			ResourceCount:     item.ResourceCount,
			TextbookCount:     item.TextbookCount,
			CoverVersion:      item.CoverVersion,
			Version:           item.Version,
		}
		if item.CoverTextbookID != "" {
			if cover, err := a.TextbookResourceURL(ctx, item.CoverTextbookID, "cover", 0); err == nil {
				view.CoverURL = cover.URL
			}
		}
		views = append(views, view)
	}
	return views, nil
}

// ResourceCenterVersion returns the revision shared by every resource-centre
// response, so clients can skip a full refresh when nothing changed.
func (a *App) ResourceCenterVersion(ctx context.Context) (string, error) {
	return a.Store.ResourceCenterRevision(ctx)
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
func (a *App) ListCourseResourcesForUser(ctx context.Context, courseCode string) ([]ResourceView, int, string, error) {
	code, err := normalizeCourseCode(courseCode)
	if err != nil {
		return nil, 0, "", err
	}
	// Read the version before the rows: a concurrent write then leaves the
	// version older than the payload, so the client refreshes next time instead
	// of caching stale rows under a newer version.
	version, err := a.Store.CourseContentRevision(ctx, code)
	if err != nil {
		return nil, 0, "", err
	}
	items := make([]sqlite.Resource, 0, 64)
	const pageSize = 200
	for offset := 0; ; offset += pageSize {
		page, err := a.Store.ListResources(ctx, pageSize, offset, "", code, "", "", false)
		if err != nil {
			return nil, 0, "", err
		}
		items = append(items, page...)
		if len(page) < pageSize {
			break
		}
	}
	total, err := a.Store.CountResources(ctx, "", code, false)
	if err != nil {
		return nil, 0, "", err
	}
	views := make([]ResourceView, 0, len(items))
	for _, item := range items {
		view := resourceView(item)
		view.SizeBytes = a.objectSize(ctx, item.PdfKey)
		views = append(views, view)
	}
	return views, total, version, nil
}

// ListCourseTextbooksForUser returns the textbooks of one course for a signed-in
// app user. A textbook associated with several courses appears in each.
func (a *App) ListCourseTextbooksForUser(ctx context.Context, courseCode string) ([]TextbookView, int, string, error) {
	code, err := normalizeCourseCode(courseCode)
	if err != nil {
		return nil, 0, "", err
	}
	version, err := a.Store.CourseContentRevision(ctx, code)
	if err != nil {
		return nil, 0, "", err
	}
	grouped, err := a.Store.ListTextbooksForCourses(ctx, []string{code})
	if err != nil {
		return nil, 0, "", err
	}
	items := grouped[code]
	views := make([]TextbookView, 0, len(items))
	for _, item := range items {
		view := textbookView(item)
		view.SizeBytes = a.objectSize(ctx, item.PdfKey)
		views = append(views, view)
	}
	return views, len(views), version, nil
}

// objectSize returns the stored PDF's byte size for the app file rows. A
// missing storage configuration or a failed HEAD request only hides the size.
func (a *App) objectSize(ctx context.Context, key string) int64 {
	if key == "" {
		return 0
	}
	a.objectSizeMu.Lock()
	if size, ok := a.objectSizeCache[key]; ok {
		a.objectSizeMu.Unlock()
		return size
	}
	a.objectSizeMu.Unlock()
	client := a.cosClient()
	if !client.Configured() {
		return 0
	}
	size, err := client.HeadObject(ctx, key)
	if err != nil {
		a.Logger.Warn("read object size failed", "error", err, "key", key)
		return 0
	}
	a.objectSizeMu.Lock()
	if a.objectSizeCache == nil {
		a.objectSizeCache = make(map[string]int64)
	}
	a.objectSizeCache[key] = size
	a.objectSizeMu.Unlock()
	return size
}
