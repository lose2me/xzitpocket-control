package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

// CourseBookInput is one textbook row submitted with a course.
type CourseBookInput struct {
	Name string `json:"name"`
	Tags string `json:"tags"`
}

// CourseUploadItem is one course row submitted by a client. The course number,
// its display name and the books it uses travel here; everything else stays on
// the client.
type CourseUploadItem struct {
	Code  string            `json:"code"`
	Name  string            `json:"name"`
	Books []CourseBookInput `json:"books"`
}

// CourseUploadInput is the batch payload sent when a client logs in.
type CourseUploadInput struct {
	Items []CourseUploadItem `json:"items"`
}

// CourseBookView is the administrator-facing book row.
type CourseBookView struct {
	Name string `json:"name"`
	Tags string `json:"tags"`
}

// CourseView is the administrator-facing course row. Books are read-only here;
// they are managed from the textbook (book list) page.
type CourseView struct {
	ID        string           `json:"id"`
	Code      string           `json:"code"`
	Name      string           `json:"name"`
	Books     []CourseBookView `json:"books"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
}

// CourseWriteInput is the administrator create/update payload.
type CourseWriteInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

const (
	maxCourseBatch = 200
	maxCourseBooks = 50
)

// UploadCourses validates and stores a client course upload and the textbooks
// each course uses. Course numbers are unique; existing courses and existing
// textbook associations are never overwritten.
func (a *App) UploadCourses(ctx context.Context, p SessionPrincipal, in CourseUploadInput) (map[string]any, error) {
	if len(in.Items) == 0 || len(in.Items) > maxCourseBatch {
		return nil, Err("invalid_courses", "课程数量必须为 1-200", 400)
	}
	courses := make([]sqlite.CourseInput, 0, len(in.Items))
	textbooks := make([]sqlite.TextbookInput, 0, len(in.Items))
	seen := make(map[string]bool, len(in.Items))
	for _, item := range in.Items {
		item.Code = strings.TrimSpace(item.Code)
		item.Name = strings.TrimSpace(item.Name)
		if item.Code == "" || len(item.Code) > 64 || strings.ContainsAny(item.Code, "\r\n\x00") {
			return nil, Err("invalid_courses", "课程编号无效", 400)
		}
		if item.Name == "" || len(item.Name) > 256 || strings.ContainsAny(item.Name, "\r\n\x00") {
			return nil, Err("invalid_courses", "课程名称无效", 400)
		}
		if len(item.Books) > maxCourseBooks {
			return nil, Err("invalid_courses", "教材数量过多", 400)
		}
		if seen[item.Code] {
			continue
		}
		seen[item.Code] = true
		courses = append(courses, sqlite.CourseInput{Code: item.Code, Name: item.Name})
		for _, book := range item.Books {
			book.Name = strings.TrimSpace(book.Name)
			book.Tags = strings.TrimSpace(book.Tags)
			if book.Name == "" || len(book.Name) > 256 || strings.ContainsAny(book.Name, "\r\n\x00") {
				return nil, Err("invalid_courses", "教材名称无效", 400)
			}
			if len(book.Tags) > 1024 || strings.ContainsAny(book.Tags, "\r\n\x00") {
				return nil, Err("invalid_courses", "教材标签无效", 400)
			}
			textbooks = append(textbooks, sqlite.TextbookInput{Name: book.Name, Tags: book.Tags, CourseCode: item.Code})
		}
	}
	if len(courses) == 0 {
		return nil, Err("invalid_courses", "课程数量必须为 1-200", 400)
	}
	now := time.Now().UTC()
	accepted, err := a.Store.UploadCourses(ctx, courses, now)
	if err != nil {
		a.Logger.Warn("upload courses failed", "error", err)
		return nil, err
	}
	acceptedBooks, err := a.Store.UploadTextbooks(ctx, textbooks, now)
	if err != nil {
		a.Logger.Warn("upload textbooks failed", "error", err)
		return nil, err
	}
	_ = a.Store.AddAudit(ctx, p.User.ID, "course_upload", "course", "",
		controlcrypto.JSON(map[string]any{"accepted": accepted, "submitted": len(courses), "books": acceptedBooks}), now)
	return map[string]any{"accepted": accepted, "accepted_books": acceptedBooks}, nil
}

func courseView(item sqlite.Course, books []sqlite.Textbook) CourseView {
	views := make([]CourseBookView, 0, len(books))
	for _, book := range books {
		views = append(views, CourseBookView{Name: book.Name, Tags: book.Tags})
	}
	return CourseView{
		ID: item.ID, Code: item.Code, Name: item.Name, Books: views,
		CreatedAt: item.CreatedAt.Format(time.RFC3339), UpdatedAt: configVersion(item.UpdatedAt),
	}
}

func (a *App) ListCoursesForAdmin(ctx context.Context, limit, offset int, search, sort, order string) ([]CourseView, int, error) {
	items, err := a.Store.ListCourses(ctx, clampLimit(limit), max0(offset), search, sort, order)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountCourses(ctx, search)
	if err != nil {
		return nil, 0, err
	}
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	booksByCode, err := a.Store.ListTextbooksForCourses(ctx, codes)
	if err != nil {
		return nil, 0, err
	}
	views := make([]CourseView, 0, len(items))
	for _, item := range items {
		views = append(views, courseView(item, booksByCode[item.Code]))
	}
	return views, total, nil
}

func validateCourseWriteInput(in CourseWriteInput) (sqlite.CourseInput, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if in.Code == "" || len(in.Code) > 64 || strings.ContainsAny(in.Code, "\r\n\x00") {
		return sqlite.CourseInput{}, Err("invalid_courses", "课程编号无效", 400)
	}
	if in.Name == "" || len(in.Name) > 256 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return sqlite.CourseInput{}, Err("invalid_courses", "课程名称无效", 400)
	}
	return sqlite.CourseInput{Code: in.Code, Name: in.Name}, nil
}

func (a *App) CreateCourseForAdmin(ctx context.Context, in CourseWriteInput, actor string) (CourseView, error) {
	input, err := validateCourseWriteInput(in)
	if err != nil {
		return CourseView{}, err
	}
	now := time.Now().UTC()
	item, err := a.Store.CreateCourse(ctx, input, now)
	if err != nil {
		if err == sqlite.ErrCourseExists {
			return CourseView{}, Err("course_exists", "该课程编号已存在", 409)
		}
		return CourseView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "course_create", "course", item.ID, "{}", now)
	return courseView(item, nil), nil
}

func (a *App) UpdateCourseForAdmin(ctx context.Context, id string, in CourseWriteInput, actor string) (CourseView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return CourseView{}, Err("invalid_course_id", "课程标识无效", 400)
	}
	input, err := validateCourseWriteInput(in)
	if err != nil {
		return CourseView{}, err
	}
	now := time.Now().UTC()
	if err := a.Store.UpdateCourse(ctx, id, input, now); err != nil {
		if err == sqlite.ErrCourseExists {
			return CourseView{}, Err("course_exists", "该课程编号已存在", 409)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return CourseView{}, ErrNotFound
		}
		return CourseView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "course_update", "course", id, "{}", now)
	item, err := a.Store.GetCourse(ctx, id)
	if err != nil {
		return CourseView{}, err
	}
	booksByCode, err := a.Store.ListTextbooksForCourses(ctx, []string{item.Code})
	if err != nil {
		return CourseView{}, err
	}
	return courseView(item, booksByCode[item.Code]), nil
}

func (a *App) DeleteCourseForAdmin(ctx context.Context, id, actor string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return Err("invalid_course_id", "课程标识无效", 400)
	}
	if err := a.Store.DeleteCourse(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return a.Store.AddAudit(ctx, actor, "course_delete", "course", id, "{}", time.Now().UTC())
}
