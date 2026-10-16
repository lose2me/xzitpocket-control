package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

// TextbookWriteInput is the administrator create/update payload. The course
// association is by course code and can be changed here without touching the
// book fields.
type TextbookWriteInput struct {
	Name       string `json:"name"`
	Tags       string `json:"tags"`
	CourseCode string `json:"course_code"`
}

// TextbookView is the administrator-facing textbook row.
type TextbookView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Tags       string `json:"tags"`
	CourseCode string `json:"course_code"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func textbookView(item sqlite.Textbook) TextbookView {
	return TextbookView{
		ID: item.ID, Name: item.Name, Tags: item.Tags, CourseCode: item.CourseCode,
		CreatedAt: item.CreatedAt.Format(time.RFC3339), UpdatedAt: configVersion(item.UpdatedAt),
	}
}

func (a *App) ListTextbooksForAdmin(ctx context.Context, limit, offset int, search, sort, order string) ([]TextbookView, int, error) {
	items, err := a.Store.ListTextbooks(ctx, clampLimit(limit), max0(offset), search, sort, order)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountTextbooks(ctx, search)
	if err != nil {
		return nil, 0, err
	}
	views := make([]TextbookView, 0, len(items))
	for _, item := range items {
		views = append(views, textbookView(item))
	}
	return views, total, nil
}

func validateTextbookWriteInput(in TextbookWriteInput) (sqlite.TextbookInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Tags = strings.TrimSpace(in.Tags)
	in.CourseCode = strings.TrimSpace(in.CourseCode)
	if in.Name == "" || len(in.Name) > 256 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return sqlite.TextbookInput{}, Err("invalid_textbook", "教材名称无效", 400)
	}
	if len(in.Tags) > 1024 || strings.ContainsAny(in.Tags, "\r\n\x00") {
		return sqlite.TextbookInput{}, Err("invalid_textbook", "教材标签无效", 400)
	}
	if len(in.CourseCode) > 64 || strings.ContainsAny(in.CourseCode, "\r\n\x00") {
		return sqlite.TextbookInput{}, Err("invalid_textbook", "关联课程编号无效", 400)
	}
	return sqlite.TextbookInput{Name: in.Name, Tags: in.Tags, CourseCode: in.CourseCode}, nil
}

func (a *App) CreateTextbookForAdmin(ctx context.Context, in TextbookWriteInput, actor string) (TextbookView, error) {
	input, err := validateTextbookWriteInput(in)
	if err != nil {
		return TextbookView{}, err
	}
	now := time.Now().UTC()
	item, err := a.Store.CreateTextbook(ctx, input, now)
	if err != nil {
		if err == sqlite.ErrTextbookExists {
			return TextbookView{}, Err("textbook_exists", "该教材已存在", 409)
		}
		return TextbookView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "textbook_create", "textbook", item.ID, "{}", now)
	return textbookView(item), nil
}

func (a *App) UpdateTextbookForAdmin(ctx context.Context, id string, in TextbookWriteInput, actor string) (TextbookView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return TextbookView{}, Err("invalid_textbook_id", "教材标识无效", 400)
	}
	input, err := validateTextbookWriteInput(in)
	if err != nil {
		return TextbookView{}, err
	}
	now := time.Now().UTC()
	if err := a.Store.UpdateTextbook(ctx, id, input, now); err != nil {
		if err == sqlite.ErrTextbookExists {
			return TextbookView{}, Err("textbook_exists", "该教材已存在", 409)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return TextbookView{}, ErrNotFound
		}
		return TextbookView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "textbook_update", "textbook", id, "{}", now)
	item, err := a.Store.GetTextbook(ctx, id)
	if err != nil {
		return TextbookView{}, err
	}
	return textbookView(item), nil
}

func (a *App) DeleteTextbookForAdmin(ctx context.Context, id, actor string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return Err("invalid_textbook_id", "教材标识无效", 400)
	}
	if err := a.Store.DeleteTextbook(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return a.Store.AddAudit(ctx, actor, "textbook_delete", "textbook", id, "{}", time.Now().UTC())
}
