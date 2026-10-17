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

// ResourceView is the administrator-facing resource row.
type ResourceView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Tags          string `json:"tags"`
	CourseCode    string `json:"course_code"`
	HasPdf        bool   `json:"has_pdf"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	DownloadCount int64  `json:"download_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// ResourceURLView is a temporary, signed CDN URL for a resource PDF.
type ResourceURLView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// ResourceUpload is one file in a course resource upload.
type ResourceUpload struct {
	Name string
	Data []byte
}

const (
	maxResourcePdfBytes = 80 << 20
	maxResourceUploads  = 20
)

func resourceView(item sqlite.Resource) ResourceView {
	return ResourceView{
		ID: item.ID, Name: item.Name, Tags: item.Tags, CourseCode: item.CourseCode,
		HasPdf:        item.PdfKey != "",
		DownloadCount: item.DownloadCount,
		CreatedAt:     item.CreatedAt.Format(time.RFC3339), UpdatedAt: configVersion(item.UpdatedAt),
	}
}

func (a *App) ListResourcesForAdmin(ctx context.Context, limit, offset int, search, courseCode, sort, order string) ([]ResourceView, int, error) {
	items, err := a.Store.ListResources(ctx, clampLimit(limit), max0(offset), search, courseCode, sort, order)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountResources(ctx, search, courseCode)
	if err != nil {
		return nil, 0, err
	}
	views := make([]ResourceView, 0, len(items))
	for _, item := range items {
		views = append(views, resourceView(item))
	}
	return views, total, nil
}

// resourceFileName keeps just the file name of an uploaded part so a crafted
// path can never escape the display name stored for a resource.
func resourceFileName(raw string) string {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if index := strings.LastIndex(raw, "/"); index >= 0 {
		raw = raw[index+1:]
	}
	raw = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	raw = strings.TrimSpace(raw)
	if len(raw) > 256 {
		raw = raw[:256]
	}
	if raw == "" {
		raw = "resource.pdf"
	}
	return raw
}

// UploadCourseResources stores each uploaded PDF under the given course. Every
// valid file becomes one resource row named after the file; invalid files are
// skipped and reported instead of failing the whole batch.
func (a *App) UploadCourseResources(ctx context.Context, courseCode string, uploads []ResourceUpload, actor string) (map[string]any, error) {
	courseCode = strings.TrimSpace(courseCode)
	if courseCode == "" || len(courseCode) > 64 || strings.ContainsAny(courseCode, "\r\n\x00") {
		return nil, Err("invalid_resource", "关联课程编号无效", 400)
	}
	if len(uploads) == 0 {
		return nil, Err("invalid_upload", "请选择至少一个 PDF 文件", 400)
	}
	if len(uploads) > maxResourceUploads {
		return nil, Err("invalid_upload", fmt.Sprintf("一次最多上传 %d 个文件", maxResourceUploads), 400)
	}
	client, err := a.requireStorage()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	items := make([]ResourceView, 0, len(uploads))
	failed := make([]map[string]string, 0)
	for _, upload := range uploads {
		name := resourceFileName(upload.Name)
		if len(upload.Data) == 0 || len(upload.Data) > maxResourcePdfBytes || !bytes.HasPrefix(upload.Data, []byte("%PDF")) {
			failed = append(failed, map[string]string{"name": name, "reason": "PDF 内容无效"})
			continue
		}
		item, err := a.Store.CreateResource(ctx, sqlite.ResourceInput{Name: name, CourseCode: courseCode}, now)
		if err != nil {
			if err == sqlite.ErrResourceExists {
				failed = append(failed, map[string]string{"name": name, "reason": "该课程下已有同名试卷"})
				continue
			}
			return nil, err
		}
		key := fmt.Sprintf("resources/%s/original.pdf", item.ID)
		if err := client.PutObject(ctx, key, "application/pdf", upload.Data); err != nil {
			a.Logger.Error("upload resource pdf failed", "error", err, "resource", item.ID)
			_ = a.Store.DeleteResource(ctx, item.ID)
			failed = append(failed, map[string]string{"name": name, "reason": "上传失败"})
			continue
		}
		if err := a.Store.SetResourcePdfKey(ctx, item.ID, key, now); err != nil {
			_ = a.Store.DeleteResource(ctx, item.ID)
			return nil, err
		}
		item.PdfKey = key
		a.purgeCDN(ctx, key)
		_ = a.Store.AddAudit(ctx, actor, "resource_upload", "resource", item.ID, "{}", now)
		items = append(items, resourceView(item))
	}
	return map[string]any{"items": items, "failed": failed, "course_code": courseCode}, nil
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
		return sqlite.Resource{}, Err("invalid_resource_id", "试卷标识无效", 400)
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

// ResourceURL returns a temporary signed CDN URL for a resource PDF.
func (a *App) ResourceURL(ctx context.Context, id string, ttl time.Duration) (ResourceURLView, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceURLView{}, err
	}
	if item.PdfKey == "" {
		return ResourceURLView{}, Err("resource_missing", "该试卷没有 PDF", 404)
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
