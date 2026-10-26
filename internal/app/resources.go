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
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Tags                 string `json:"tags"`
	CourseCode           string `json:"course_code"`
	HasPdf               bool   `json:"has_pdf"`
	Format               string `json:"format"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	DownloadCount        int64  `json:"download_count"`
	ContributorUserID    string `json:"contributor_user_id,omitempty"`
	ContributorStudentID string `json:"contributor_student_id,omitempty"`
	ContributorName      string `json:"contributor_name,omitempty"`
	Archived             bool   `json:"archived"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
	HasAI                bool   `json:"has_ai"`
	HasBasicAI           bool   `json:"has_basic_ai"`
	HasAdvancedAI        bool   `json:"has_advanced_ai"`
	HasManualAI          bool   `json:"has_manual_ai"`
	manualUpdatedAt      time.Time
	AIModes              []string `json:"ai_modes,omitempty"`
	AISolvable           int      `json:"ai_solvable"`
	AIUpdatedAt          string   `json:"ai_updated_at,omitempty"`
}

// ResourceURLView is a temporary, signed CDN URL for a resource PDF.
type ResourceURLView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// ResourceAIView is the app-facing AI analysis of one paper.
type ResourceAIView struct {
	Available    bool   `json:"available"`
	Mode         string `json:"mode"`
	Status       string `json:"status,omitempty"`
	Markdown     string `json:"markdown,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	OutputSize   int64  `json:"output_size,omitempty"`
	TotalPages   int    `json:"total_pages,omitempty"`
	Solvable     int    `json:"solvable,omitempty"`
	PrecheckNote string `json:"precheck_note,omitempty"`
	PDFURL       string `json:"pdf_url,omitempty"`
	Error        string `json:"error,omitempty"`
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
		HasPdf:          item.PdfKey != "",
		Format:          storedFileFormat(item.PdfKey),
		DownloadCount:   item.DownloadCount,
		Archived:        !item.ArchivedAt.IsZero(),
		HasManualAI:     item.ManualAIKey != "",
		manualUpdatedAt: item.ManualAIUpdatedAt,
		AISolvable:      -1,
		CreatedAt:       item.CreatedAt.Format(time.RFC3339), UpdatedAt: configVersion(item.UpdatedAt),
	}
}

// resourceViewForAdmin adds the contributor's decrypted student ID. It is only
// used by administrator endpoints; app-facing rows leave these fields empty.
func (a *App) resourceViewForAdmin(ctx context.Context, item sqlite.Resource) ResourceView {
	view := resourceView(item)
	view.ContributorUserID = item.ContributorUserID
	if item.ContributorUserID != "" {
		view.ContributorStudentID, view.ContributorName = a.contributorInfo(ctx, item.ContributorUserID)
	}
	return view
}

// enrichAIStatus fills the per-resource AI flags used by the console and the
// app resource lists.
func (a *App) enrichAIStatus(ctx context.Context, views []ResourceView) error {
	if len(views) == 0 {
		return nil
	}
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	succeeded, err := a.Store.LatestSucceededAITasks(ctx, ids)
	if err != nil {
		return err
	}
	latest, err := a.Store.LatestAITasks(ctx, ids)
	if err != nil {
		return err
	}
	for i := range views {
		byMode := succeeded[views[i].ID]
		modes := make([]string, 0, 3)
		var newest time.Time
		if task, ok := byMode["basic"]; ok {
			views[i].HasBasicAI = true
			modes = append(modes, "basic")
			if task.UpdatedAt.After(newest) {
				newest = task.UpdatedAt
			}
		}
		if task, ok := byMode["advanced"]; ok {
			views[i].HasAdvancedAI = true
			modes = append(modes, "advanced")
			if task.UpdatedAt.After(newest) {
				newest = task.UpdatedAt
			}
		}
		if views[i].HasManualAI {
			modes = append(modes, "manual")
			if views[i].manualUpdatedAt.After(newest) {
				newest = views[i].manualUpdatedAt
			}
		}
		views[i].AIModes = modes
		views[i].HasAI = len(modes) > 0
		if !newest.IsZero() {
			views[i].AIUpdatedAt = newest.Format(time.RFC3339)
		}
		if task, ok := latest[views[i].ID]; ok {
			views[i].AISolvable = task.Solvable
		}
	}
	return nil
}

func (a *App) ListResourcesForAdmin(ctx context.Context, limit, offset int, search, courseCode, sort, order string, archived bool) ([]ResourceView, int, error) {
	items, err := a.Store.ListResources(ctx, clampLimit(limit), max0(offset), search, courseCode, sort, order, archived)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountResources(ctx, search, courseCode, archived)
	if err != nil {
		return nil, 0, err
	}
	views := make([]ResourceView, 0, len(items))
	for _, item := range items {
		views = append(views, a.resourceViewForAdmin(ctx, item))
	}
	if err := a.enrichAIStatus(ctx, views); err != nil {
		return nil, 0, err
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
		key := publishedObjectKey("resource", fileFormatPDF, md5Hex(upload.Data))
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

// UploadManualAnalysis stores a manually prepared analysis PDF for a paper.
// It replaces any previous manual file.
func (a *App) UploadManualAnalysis(ctx context.Context, id string, data []byte, actor string) (ResourceView, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceView{}, err
	}
	if len(data) == 0 || len(data) > maxResourcePdfBytes || !bytes.HasPrefix(data, []byte("%PDF")) {
		return ResourceView{}, Err("invalid_upload", "PDF 内容无效", 400)
	}
	client, err := a.requireStorage()
	if err != nil {
		return ResourceView{}, err
	}
	now := time.Now().UTC()
	key := publishedObjectKey("resource", fileFormatPDF, md5Hex([]byte(item.ID+":"+md5Hex(data))))
	if err := client.PutObject(ctx, key, "application/pdf", data); err != nil {
		return ResourceView{}, err
	}
	oldKey := item.ManualAIKey
	if err := a.Store.SetResourceManualAnalysis(ctx, item.ID, key, int64(len(data)), now); err != nil {
		_ = client.DeleteObject(ctx, key)
		return ResourceView{}, err
	}
	if oldKey != "" && oldKey != key {
		a.removeStoredFileIfUnused(ctx, oldKey)
	}
	a.purgeCDN(ctx, key)
	_ = a.Store.AddAudit(ctx, actor, "resource_manual_analysis", "resource", item.ID, "{}", now)

	item.ManualAIKey = key
	item.ManualAISize = int64(len(data))
	item.ManualAIUpdatedAt = now
	views := []ResourceView{resourceView(item)}
	if err := a.enrichAIStatus(ctx, views); err != nil {
		return ResourceView{}, err
	}
	return views[0], nil
}

func (a *App) DeleteResourceForAdmin(ctx context.Context, id, actor string) error {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return err
	}
	aiOutputKeys := make([]string, 0)
	manualKey := item.ManualAIKey
	if tasks, taskErr := a.Store.ListAITasksByResource(ctx, item.ID); taskErr == nil {
		for _, task := range tasks {
			if task.OutputKey != "" {
				aiOutputKeys = append(aiOutputKeys, task.OutputKey)
			}
			a.cancelAIRequest(task.ID)
		}
	}
	if err := a.Store.DeleteResource(ctx, item.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	a.removeResourceAssets(ctx, item)
	if manualKey != "" {
		a.removeStoredFileIfUnused(ctx, manualKey)
	}
	if len(aiOutputKeys) > 0 {
		if client, err := a.requireStorage(); err == nil {
			for _, key := range aiOutputKeys {
				if err := client.DeleteObject(ctx, key); err != nil {
					a.Logger.Warn("delete AI analysis output", "error", err, "key", key)
				}
			}
		}
	}
	if item.ContributorUserID != "" {
		a.transferAttribution(ctx, item.ContributorUserID, "", item.DownloadCount)
	}
	return a.Store.AddAudit(ctx, actor, "resource_delete", "resource", item.ID, "{}", time.Now().UTC())
}

// SetResourceArchived moves an exam between the published and archived COS
// areas. Contributor and download counters stay attached to the row.
func (a *App) SetResourceArchived(ctx context.Context, id string, archived bool, actor string) (ResourceView, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceView{}, err
	}
	if archived == !item.ArchivedAt.IsZero() {
		return a.resourceViewForAdmin(ctx, item), nil
	}
	newPdfKey := item.PdfKey
	if newPdfKey != "" {
		if hash := objectHashFromKey(newPdfKey); hash != "" {
			if archived {
				newPdfKey = archivedObjectKey("resource", storedFileFormat(newPdfKey), hash)
			} else {
				newPdfKey = publishedObjectKey("resource", storedFileFormat(newPdfKey), hash)
			}
		}
	}
	if newPdfKey != item.PdfKey {
		if err := a.copyStoredObject(ctx, item.PdfKey, newPdfKey); err != nil {
			return ResourceView{}, err
		}
	}
	now := time.Now().UTC()
	archivedAt := time.Time{}
	if archived {
		archivedAt = now
	}
	oldPdfKey := item.PdfKey
	if err := a.Store.SetResourceArchive(ctx, item.ID, newPdfKey, archivedAt); err != nil {
		return ResourceView{}, err
	}
	item.PdfKey, item.ArchivedAt = newPdfKey, archivedAt
	if oldPdfKey != newPdfKey {
		a.removeStoredFileIfUnused(ctx, oldPdfKey)
	}
	a.purgeCDN(ctx, newPdfKey)
	action := "resource_archive"
	if !archived {
		action = "resource_restore"
	}
	_ = a.Store.AddAudit(ctx, actor, action, "resource", item.ID, "{}", now)
	return a.resourceViewForAdmin(ctx, item), nil
}

// removeResourceAssets deletes a removed resource's PDF when no other row
// references the content-addressed key. It is best-effort: the row is already
// gone, so failures are logged rather than surfaced.
func (a *App) removeResourceAssets(ctx context.Context, item sqlite.Resource) {
	a.removeStoredFileIfUnused(ctx, item.PdfKey)
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

// ResourceURL returns a temporary signed CDN URL for a published resource.
func (a *App) ResourceURL(ctx context.Context, id string, ttl time.Duration) (ResourceURLView, error) {
	return a.resourceURL(ctx, id, ttl, false)
}

// ResourceAIForUser returns the newest succeeded analysis for a published
// resource, including its markdown and a temporary URL for the rendered PDF.
// A resource that has been queued but has no finished run reports its status
// so the app can show "解析中".
func (a *App) ResourceAIForUser(ctx context.Context, id, mode string, ttl time.Duration) (ResourceAIView, error) {
	if mode != "basic" && mode != "advanced" && mode != "manual" {
		mode = "basic"
	}
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceAIView{}, err
	}
	if !item.ArchivedAt.IsZero() {
		return ResourceAIView{}, ErrNotFound
	}
	if mode == "manual" {
		if item.ManualAIKey == "" {
			return ResourceAIView{Mode: mode}, nil
		}
		view := ResourceAIView{
			Available:  true,
			Mode:       mode,
			UpdatedAt:  item.ManualAIUpdatedAt.Format(time.RFC3339),
			OutputSize: item.ManualAISize,
		}
		if a.cfg().CDNDomain != "" && a.cfg().CDNAuthKey != "" {
			if ttl <= 0 || ttl > 7*24*time.Hour {
				ttl = a.cfg().CDNURLTTL
			}
			view.PDFURL = cloud.CDNSignedURL(a.cfg().CDNDomain, a.cfg().CDNAuthKey, "/"+item.ManualAIKey, ttl)
		}
		return view, nil
	}
	tasks, err := a.Store.ListAITasksByResource(ctx, id)
	if err != nil {
		return ResourceAIView{}, err
	}
	filtered := make([]sqlite.AITask, 0, len(tasks))
	for _, task := range tasks {
		if task.Mode == mode {
			filtered = append(filtered, task)
		}
	}
	if len(filtered) == 0 {
		return ResourceAIView{Mode: mode}, nil
	}
	latest := filtered[len(filtered)-1]
	var succeeded *sqlite.AITask
	for i := len(filtered) - 1; i >= 0; i-- {
		if filtered[i].Status == "succeeded" {
			succeeded = &filtered[i]
			break
		}
	}
	if succeeded == nil {
		return ResourceAIView{Mode: mode, Status: latest.Status, Error: latest.Error}, nil
	}
	view := ResourceAIView{
		Available:    true,
		Mode:         mode,
		Status:       succeeded.Status,
		Markdown:     succeeded.Markdown,
		UpdatedAt:    succeeded.UpdatedAt.Format(time.RFC3339),
		OutputSize:   succeeded.OutputSize,
		TotalPages:   succeeded.TotalPages,
		Solvable:     succeeded.Solvable,
		PrecheckNote: succeeded.PrecheckNote,
	}
	if succeeded.OutputKey != "" && a.cfg().CDNDomain != "" && a.cfg().CDNAuthKey != "" {
		if ttl <= 0 || ttl > 7*24*time.Hour {
			ttl = a.cfg().CDNURLTTL
		}
		view.PDFURL = cloud.CDNSignedURL(a.cfg().CDNDomain, a.cfg().CDNAuthKey, "/"+succeeded.OutputKey, ttl)
	}
	return view, nil
}

// ResourceURLForAdmin also serves archived resources in the console.
func (a *App) ResourceURLForAdmin(ctx context.Context, id string, ttl time.Duration) (ResourceURLView, error) {
	return a.resourceURL(ctx, id, ttl, true)
}

func (a *App) resourceURL(ctx context.Context, id string, ttl time.Duration, allowArchived bool) (ResourceURLView, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceURLView{}, err
	}
	if !allowArchived && !item.ArchivedAt.IsZero() {
		return ResourceURLView{}, ErrNotFound
	}
	if item.PdfKey == "" {
		return ResourceURLView{}, Err("resource_missing", "该试卷没有文件", 404)
	}
	if a.cfg().CDNDomain == "" || a.cfg().CDNAuthKey == "" {
		return ResourceURLView{}, Err("cdn_unavailable", "CDN 未配置", 503)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = a.cfg().CDNURLTTL
	}
	url := cloud.CDNSignedURL(a.cfg().CDNDomain, a.cfg().CDNAuthKey, "/"+item.PdfKey, ttl)
	return ResourceURLView{
		URL:       url,
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
	}, nil
}
