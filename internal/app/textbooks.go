package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"xzitpocket-control/internal/cloud"
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
	HasPdf     bool   `json:"has_pdf"`
	HasCover   bool   `json:"has_cover"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// TextbookResourceView is a temporary, signed CDN URL for a textbook asset.
type TextbookResourceView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

const (
	maxTextbookPdfBytes   = 80 << 20
	maxTextbookCoverBytes = 2 << 20
)

func textbookView(item sqlite.Textbook) TextbookView {
	return TextbookView{
		ID: item.ID, Name: item.Name, Tags: item.Tags, CourseCode: item.CourseCode,
		HasPdf: item.PdfKey != "", HasCover: item.CoverKey != "",
		CreatedAt: item.CreatedAt.Format(time.RFC3339), UpdatedAt: configVersion(item.UpdatedAt),
	}
}

func (a *App) cosClient() *cloud.COSClient {
	return cloud.NewCOSClient(a.Cfg.COSSecretID, a.Cfg.COSSecretKey, a.Cfg.COSBucket, a.Cfg.COSRegion)
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
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return err
	}
	if err := a.Store.DeleteTextbook(ctx, item.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	a.removeTextbookAssets(ctx, item)
	return a.Store.AddAudit(ctx, actor, "textbook_delete", "textbook", item.ID, "{}", time.Now().UTC())
}

// purgeCDN invalidates cached copies of the given object keys. It is
// best-effort: the object is already stored, and refreshes wear off once the
// cache TTL expires even when the API call fails.
func (a *App) purgeCDN(ctx context.Context, keys ...string) {
	if a.Cfg.CDNDomain == "" || a.Cfg.COSSecretID == "" || a.Cfg.COSSecretKey == "" {
		return
	}
	urls := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		urls = append(urls, a.Cfg.CDNDomain+"/"+strings.TrimPrefix(key, "/"))
	}
	if len(urls) == 0 {
		return
	}
	if err := cloud.PurgeCDNCache(ctx, a.Cfg.COSSecretID, a.Cfg.COSSecretKey, urls); err != nil {
		a.Logger.Warn("cdn purge failed", "error", err, "urls", urls)
	}
}

// removeTextbookAssets deletes a removed textbook's PDF/cover from object
// storage and drops their CDN copies. It is best-effort: the row is already
// gone, so a storage failure is logged rather than surfaced.
func (a *App) removeTextbookAssets(ctx context.Context, item sqlite.Textbook) {
	keys := make([]string, 0, 2)
	for _, key := range []string{item.PdfKey, item.CoverKey} {
		if key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	if client := a.cosClient(); client.Configured() {
		for _, key := range keys {
			if err := client.DeleteObject(ctx, key); err != nil {
				a.Logger.Warn("delete textbook object failed", "error", err, "key", key)
			}
		}
	}
	a.purgeCDN(ctx, keys...)
}

func (a *App) requireTextbook(ctx context.Context, id string) (sqlite.Textbook, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return sqlite.Textbook{}, Err("invalid_textbook_id", "教材标识无效", 400)
	}
	item, err := a.Store.GetTextbook(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sqlite.Textbook{}, ErrNotFound
		}
		return sqlite.Textbook{}, err
	}
	return item, nil
}

func (a *App) requireStorage() (*cloud.COSClient, error) {
	client := a.cosClient()
	if !client.Configured() {
		return nil, Err("storage_unavailable", "对象存储未配置", 503)
	}
	return client, nil
}

// UploadTextbookPdf stores a textbook's PDF in object storage.
func (a *App) UploadTextbookPdf(ctx context.Context, id string, data []byte, actor string) (TextbookView, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return TextbookView{}, err
	}
	if len(data) == 0 || len(data) > maxTextbookPdfBytes || !bytes.HasPrefix(data, []byte("%PDF")) {
		return TextbookView{}, Err("invalid_pdf", "PDF 内容无效", 400)
	}
	client, err := a.requireStorage()
	if err != nil {
		return TextbookView{}, err
	}
	key := fmt.Sprintf("books/%s/original.pdf", item.ID)
	if err := client.PutObject(ctx, key, "application/pdf", data); err != nil {
		a.Logger.Error("upload textbook pdf failed", "error", err, "textbook", item.ID)
		return TextbookView{}, Err("storage_error", "上传 PDF 失败", 502)
	}
	now := time.Now().UTC()
	if err := a.Store.SetTextbookPdfKey(ctx, item.ID, key, now); err != nil {
		return TextbookView{}, err
	}
	item.PdfKey = key
	a.purgeCDN(ctx, key)
	_ = a.Store.AddAudit(ctx, actor, "textbook_pdf", "textbook", item.ID, "{}", now)
	return textbookView(item), nil
}

// UploadTextbookCover stores a browser-rendered WebP cover in object storage.
func (a *App) UploadTextbookCover(ctx context.Context, id string, data []byte, actor string) (TextbookView, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return TextbookView{}, err
	}
	if len(data) == 0 || len(data) > maxTextbookCoverBytes {
		return TextbookView{}, Err("invalid_cover", "封面内容无效", 400)
	}
	client, err := a.requireStorage()
	if err != nil {
		return TextbookView{}, err
	}
	key := fmt.Sprintf("books/%s/cover.webp", item.ID)
	if err := client.PutObject(ctx, key, "image/webp", data); err != nil {
		a.Logger.Error("upload textbook cover failed", "error", err, "textbook", item.ID)
		return TextbookView{}, Err("storage_error", "上传封面失败", 502)
	}
	now := time.Now().UTC()
	if err := a.Store.SetTextbookCoverKey(ctx, item.ID, key, now); err != nil {
		return TextbookView{}, err
	}
	item.CoverKey = key
	a.purgeCDN(ctx, key)
	_ = a.Store.AddAudit(ctx, actor, "textbook_cover", "textbook", item.ID, "{}", now)
	return textbookView(item), nil
}

// TextbookResourceURL returns a temporary signed CDN URL for a textbook asset.
// kind is "pdf" or "cover".
func (a *App) TextbookResourceURL(ctx context.Context, id, kind string, ttl time.Duration) (TextbookResourceView, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return TextbookResourceView{}, err
	}
	var key string
	switch strings.TrimSpace(kind) {
	case "", "pdf":
		key = item.PdfKey
	case "cover":
		key = item.CoverKey
	default:
		return TextbookResourceView{}, Err("invalid_kind", "资源类型无效", 400)
	}
	if key == "" {
		return TextbookResourceView{}, Err("resource_missing", "该教材没有对应资源", 404)
	}
	if a.Cfg.CDNDomain == "" || a.Cfg.CDNAuthKey == "" {
		return TextbookResourceView{}, Err("cdn_unavailable", "CDN 未配置", 503)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = a.Cfg.CDNURLTTL
	}
	url := cloud.CDNSignedURL(a.Cfg.CDNDomain, a.Cfg.CDNAuthKey, "/"+key, ttl)
	return TextbookResourceView{
		URL:       url,
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
	}, nil
}
