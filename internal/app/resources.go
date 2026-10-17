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

// ResourceWriteInput is the administrator create/update payload. The course
// association is by course code and can be changed here without touching the
// other fields.
type ResourceWriteInput struct {
	Name       string `json:"name"`
	Tags       string `json:"tags"`
	CourseCode string `json:"course_code"`
}

// ResourceView is the administrator-facing resource row.
type ResourceView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Tags       string `json:"tags"`
	CourseCode string `json:"course_code"`
	HasPdf     bool   `json:"has_pdf"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ResourceURLView is a temporary, signed CDN URL for a resource PDF.
type ResourceURLView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

const maxResourcePdfBytes = 80 << 20

func resourceView(item sqlite.Resource) ResourceView {
	return ResourceView{
		ID: item.ID, Name: item.Name, Tags: item.Tags, CourseCode: item.CourseCode,
		HasPdf: item.PdfKey != "",
		CreatedAt: item.CreatedAt.Format(time.RFC3339), UpdatedAt: configVersion(item.UpdatedAt),
	}
}

func (a *App) ListResourcesForAdmin(ctx context.Context, limit, offset int, search, sort, order string) ([]ResourceView, int, error) {
	items, err := a.Store.ListResources(ctx, clampLimit(limit), max0(offset), search, sort, order)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountResources(ctx, search)
	if err != nil {
		return nil, 0, err
	}
	views := make([]ResourceView, 0, len(items))
	for _, item := range items {
		views = append(views, resourceView(item))
	}
	return views, total, nil
}

func validateResourceWriteInput(in ResourceWriteInput) (sqlite.ResourceInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Tags = strings.TrimSpace(in.Tags)
	in.CourseCode = strings.TrimSpace(in.CourseCode)
	if in.Name == "" || len(in.Name) > 256 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return sqlite.ResourceInput{}, Err("invalid_resource", "资源名称无效", 400)
	}
	if len(in.Tags) > 1024 || strings.ContainsAny(in.Tags, "\r\n\x00") {
		return sqlite.ResourceInput{}, Err("invalid_resource", "资源标签无效", 400)
	}
	if len(in.CourseCode) > 64 || strings.ContainsAny(in.CourseCode, "\r\n\x00") {
		return sqlite.ResourceInput{}, Err("invalid_resource", "关联课程编号无效", 400)
	}
	return sqlite.ResourceInput{Name: in.Name, Tags: in.Tags, CourseCode: in.CourseCode}, nil
}

func (a *App) CreateResourceForAdmin(ctx context.Context, in ResourceWriteInput, actor string) (ResourceView, error) {
	input, err := validateResourceWriteInput(in)
	if err != nil {
		return ResourceView{}, err
	}
	now := time.Now().UTC()
	item, err := a.Store.CreateResource(ctx, input, now)
	if err != nil {
		if err == sqlite.ErrResourceExists {
			return ResourceView{}, Err("resource_exists", "该资源已存在", 409)
		}
		return ResourceView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "resource_create", "resource", item.ID, "{}", now)
	return resourceView(item), nil
}

func (a *App) UpdateResourceForAdmin(ctx context.Context, id string, in ResourceWriteInput, actor string) (ResourceView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ResourceView{}, Err("invalid_resource_id", "资源标识无效", 400)
	}
	input, err := validateResourceWriteInput(in)
	if err != nil {
		return ResourceView{}, err
	}
	now := time.Now().UTC()
	if err := a.Store.UpdateResource(ctx, id, input, now); err != nil {
		if err == sqlite.ErrResourceExists {
			return ResourceView{}, Err("resource_exists", "该资源已存在", 409)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ResourceView{}, ErrNotFound
		}
		return ResourceView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "resource_update", "resource", id, "{}", now)
	item, err := a.Store.GetResource(ctx, id)
	if err != nil {
		return ResourceView{}, err
	}
	return resourceView(item), nil
}

func (a *App) DeleteResourceForAdmin(ctx context.Context, id, actor string) error {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return err
	}
	if err := a.Store.DeleteResource(ctx, item.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	a.removeResourceAssets(ctx, item)
	return a.Store.AddAudit(ctx, actor, "resource_delete", "resource", item.ID, "{}", time.Now().UTC())
}

// removeResourceAssets deletes a removed resource's PDF from object storage and
// drops its CDN copy. It is best-effort: the row is already gone, so a storage
// failure is logged rather than surfaced.
func (a *App) removeResourceAssets(ctx context.Context, item sqlite.Resource) {
	if item.PdfKey == "" {
		return
	}
	if client := a.cosClient(); client.Configured() {
		if err := client.DeleteObject(ctx, item.PdfKey); err != nil {
			a.Logger.Warn("delete resource object failed", "error", err, "key", item.PdfKey)
		}
	}
	a.purgeCDN(ctx, item.PdfKey)
}

func (a *App) requireResource(ctx context.Context, id string) (sqlite.Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return sqlite.Resource{}, Err("invalid_resource_id", "资源标识无效", 400)
	}
	item, err := a.Store.GetResource(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sqlite.Resource{}, ErrNotFound
		}
		return sqlite.Resource{}, err
	}
	return item, nil
}

// UploadResourcePdf stores a resource's PDF in object storage.
func (a *App) UploadResourcePdf(ctx context.Context, id string, data []byte, actor string) (ResourceView, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceView{}, err
	}
	if len(data) == 0 || len(data) > maxResourcePdfBytes || !bytes.HasPrefix(data, []byte("%PDF")) {
		return ResourceView{}, Err("invalid_pdf", "PDF 内容无效", 400)
	}
	client, err := a.requireStorage()
	if err != nil {
		return ResourceView{}, err
	}
	key := fmt.Sprintf("resources/%s/original.pdf", item.ID)
	if err := client.PutObject(ctx, key, "application/pdf", data); err != nil {
		a.Logger.Error("upload resource pdf failed", "error", err, "resource", item.ID)
		return ResourceView{}, Err("storage_error", "上传 PDF 失败", 502)
	}
	now := time.Now().UTC()
	if err := a.Store.SetResourcePdfKey(ctx, item.ID, key, now); err != nil {
		return ResourceView{}, err
	}
	item.PdfKey = key
	a.purgeCDN(ctx, key)
	_ = a.Store.AddAudit(ctx, actor, "resource_pdf", "resource", item.ID, "{}", now)
	return resourceView(item), nil
}

// ResourceURL returns a temporary signed CDN URL for a resource PDF.
func (a *App) ResourceURL(ctx context.Context, id string, ttl time.Duration) (ResourceURLView, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceURLView{}, err
	}
	if item.PdfKey == "" {
		return ResourceURLView{}, Err("resource_missing", "该资源没有 PDF", 404)
	}
	if a.Cfg.CDNDomain == "" || a.Cfg.CDNAuthKey == "" {
		return ResourceURLView{}, Err("cdn_unavailable", "CDN 未配置", 503)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = a.Cfg.CDNURLTTL
	}
	url := cloud.CDNSignedURL(a.Cfg.CDNDomain, a.Cfg.CDNAuthKey, "/"+item.PdfKey, ttl)
	return ResourceURLView{
		URL:       url,
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
	}, nil
}
