package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"xzitpocket-control/internal/cloud"
	"xzitpocket-control/internal/store/sqlite"
)

// TextbookWriteInput is the administrator create/update payload. CourseCodes
// holds every associated course in primary-first order.
type TextbookWriteInput struct {
	Name        string   `json:"name"`
	Tags        string   `json:"tags"`
	CourseCodes []string `json:"course_codes"`
}

// TextbookView is the administrator-facing textbook row.
type TextbookView struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Tags                 string   `json:"tags"`
	CourseCode           string   `json:"course_code"`
	CourseCodes          []string `json:"course_codes"`
	HasPdf               bool     `json:"has_pdf"`
	FileVersion          string   `json:"file_version"`
	HasCover             bool     `json:"has_cover"`
	SizeBytes            int64    `json:"size_bytes,omitempty"`
	DownloadCount        int64    `json:"download_count"`
	ContributorUserID    string   `json:"contributor_user_id,omitempty"`
	ContributorStudentID string   `json:"contributor_student_id,omitempty"`
	ContributorName      string   `json:"contributor_name,omitempty"`
	Archived             bool     `json:"archived"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
}

// TextbookResourceView is a temporary, signed CDN URL for a textbook asset.
type TextbookResourceView struct {
	URL         string `json:"url"`
	FileVersion string `json:"file_version"`
	ExpiresAt   string `json:"expires_at"`
}

const (
	maxTextbookPdfBytes   = 80 << 20
	maxTextbookCoverBytes = 2 << 20
)

func textbookView(item sqlite.Textbook) TextbookView {
	codes := item.CourseCodes
	if codes == nil {
		codes = []string{}
	}
	return TextbookView{
		ID: item.ID, Name: item.Name, Tags: item.Tags, CourseCode: item.CourseCode, CourseCodes: codes,
		HasPdf: item.PdfKey != "", HasCover: item.CoverKey != "", DownloadCount: item.DownloadCount,
		FileVersion: storedFileVersion(item.PdfKey),
		Archived:    !item.ArchivedAt.IsZero(),
		CreatedAt:   item.CreatedAt.Format(time.RFC3339), UpdatedAt: configVersion(item.UpdatedAt),
	}
}

// normalizeCourseAssociations trims and deduplicates course codes, keeping the
// caller's order. The first code becomes the primary association.
func normalizeCourseAssociations(codes []string) ([]string, bool) {
	result := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	add := func(code string) bool {
		code = strings.TrimSpace(code)
		if code == "" {
			return true
		}
		if len(code) > 64 || strings.ContainsAny(code, "\r\n\x00") {
			return false
		}
		if !seen[code] {
			seen[code] = true
			result = append(result, code)
		}
		return true
	}
	for _, code := range codes {
		if !add(code) {
			return nil, false
		}
	}
	if len(result) > 20 {
		return nil, false
	}
	return result, true
}

func (a *App) cosClient() *cloud.COSClient {
	if a.cosClientOverride != nil {
		return a.cosClientOverride
	}
	return cloud.NewCOSClient(a.cfg().COSSecretID, a.cfg().COSSecretKey, a.cfg().COSBucket, a.cfg().COSRegion)
}

// textbookViewForAdmin adds the contributor's decrypted student ID. It is only
// used by administrator endpoints; app-facing rows leave these fields empty.
func (a *App) textbookViewForAdmin(ctx context.Context, item sqlite.Textbook) TextbookView {
	view := textbookView(item)
	view.ContributorUserID = item.ContributorUserID
	if item.ContributorUserID != "" {
		view.ContributorStudentID, view.ContributorName = a.contributorInfo(ctx, item.ContributorUserID)
	}
	return view
}

func (a *App) ListTextbooksForAdmin(ctx context.Context, limit, offset int, search, sort, order string, archived bool) ([]TextbookView, int, error) {
	items, err := a.Store.ListTextbooks(ctx, clampLimit(limit), max0(offset), search, sort, order, archived)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountTextbooks(ctx, search, archived)
	if err != nil {
		return nil, 0, err
	}
	views := make([]TextbookView, 0, len(items))
	for _, item := range items {
		views = append(views, a.textbookViewForAdmin(ctx, item))
	}
	return views, total, nil
}

func validateTextbookWriteInput(in TextbookWriteInput) (sqlite.TextbookInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Tags = strings.TrimSpace(in.Tags)
	if in.Name == "" || len(in.Name) > 256 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return sqlite.TextbookInput{}, Err("invalid_textbook", "教材名称无效", 400)
	}
	if len(in.Tags) > 1024 || strings.ContainsAny(in.Tags, "\r\n\x00") {
		return sqlite.TextbookInput{}, Err("invalid_textbook", "教材标签无效", 400)
	}
	codes, ok := normalizeCourseAssociations(in.CourseCodes)
	if !ok {
		return sqlite.TextbookInput{}, Err("invalid_textbook", "关联课程编号无效", 400)
	}
	return sqlite.TextbookInput{Name: in.Name, Tags: in.Tags, CourseCodes: codes}, nil
}

// ListTextbooksForUpload feeds the app upload picker with every textbook and
// its course associations.
func (a *App) ListTextbooksForUpload(ctx context.Context) ([]TextbookView, error) {
	items, err := a.Store.ListAllTextbooks(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]TextbookView, 0, len(items))
	for _, item := range items {
		views = append(views, textbookView(item))
	}
	return views, nil
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
	if item.ContributorUserID != "" {
		a.transferAttribution(ctx, item.ContributorUserID, "", item.DownloadCount)
	}
	return a.Store.AddAudit(ctx, actor, "textbook_delete", "textbook", item.ID, "{}", time.Now().UTC())
}

// SetTextbookArchived moves a textbook between the published and archived COS
// areas. The row, its contributor and its download totals stay untouched so an
// archived book keeps its attribution and can be restored without re-binding.
func (a *App) SetTextbookArchived(ctx context.Context, id string, archived bool, actor string) (TextbookView, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return TextbookView{}, err
	}
	if archived == !item.ArchivedAt.IsZero() {
		return a.textbookViewForAdmin(ctx, item), nil
	}
	newPdfKey, newCoverKey := textbookAreaKeys(item, archived)
	if newPdfKey != item.PdfKey {
		if err := a.copyStoredObject(ctx, item.PdfKey, newPdfKey); err != nil {
			return TextbookView{}, err
		}
	}
	if newCoverKey != item.CoverKey {
		if err := a.copyStoredObject(ctx, item.CoverKey, newCoverKey); err != nil {
			return TextbookView{}, err
		}
	}
	now := time.Now().UTC()
	archivedAt := time.Time{}
	if archived {
		archivedAt = now
	}
	oldPdfKey, oldCoverKey := item.PdfKey, item.CoverKey
	if err := a.Store.SetTextbookArchive(ctx, item.ID, newPdfKey, newCoverKey, archivedAt); err != nil {
		return TextbookView{}, err
	}
	item.PdfKey, item.CoverKey, item.ArchivedAt = newPdfKey, newCoverKey, archivedAt
	if oldPdfKey != newPdfKey {
		a.removeStoredFileIfUnused(ctx, oldPdfKey)
	}
	if oldCoverKey != newCoverKey {
		a.removeCoverIfUnused(ctx, oldCoverKey)
	}
	a.purgeCDN(ctx, newPdfKey, newCoverKey)
	action := "textbook_archive"
	if !archived {
		action = "textbook_restore"
	}
	_ = a.Store.AddAudit(ctx, actor, action, "textbook", item.ID, "{}", now)
	return a.textbookViewForAdmin(ctx, item), nil
}

// textbookAreaKeys derives the published or archived object keys for a
// textbook from its content hash.
func textbookAreaKeys(item sqlite.Textbook, archived bool) (string, string) {
	pdfKey, coverKey := item.PdfKey, item.CoverKey
	if pdfKey != "" {
		hash := objectHashFromKey(pdfKey)
		if archived {
			pdfKey = archivedObjectKey("textbook", fileFormatPDF, hash)
		} else {
			pdfKey = publishedObjectKey("textbook", fileFormatPDF, hash)
		}
	}
	if coverKey != "" {
		hash := objectHashFromKey(coverKey)
		if archived {
			coverKey = archivedTextbookCoverKey(hash)
		} else {
			coverKey = textbookCoverKey(hash)
		}
	}
	return pdfKey, coverKey
}

// copyStoredObject copies a COS object so the source can be removed once no row
// references it any more.
func (a *App) copyStoredObject(ctx context.Context, src, dst string) error {
	if src == "" || src == dst {
		return nil
	}
	client, err := a.requireStorage()
	if err != nil {
		return err
	}
	if err := client.CopyObject(ctx, src, dst); err != nil {
		a.Logger.Error("copy stored object failed", "error", err, "source", src, "destination", dst)
		return Err("storage_error", "移动存储文件失败", http.StatusBadGateway)
	}
	return nil
}

// purgeCDN invalidates cached copies of the given object keys. It is
// best-effort: the object is already stored, and refreshes wear off once the
// cache TTL expires even when the API call fails.
func (a *App) purgeCDN(ctx context.Context, keys ...string) {
	if a.cfg().CDNDomain == "" || a.cfg().COSSecretID == "" || a.cfg().COSSecretKey == "" {
		return
	}
	urls := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		urls = append(urls, a.cfg().CDNDomain+"/"+strings.TrimPrefix(key, "/"))
	}
	if len(urls) == 0 {
		return
	}
	if err := cloud.PurgeCDNCache(ctx, a.cfg().COSSecretID, a.cfg().COSSecretKey, urls); err != nil {
		a.Logger.Warn("cdn purge failed", "error", err, "urls", urls)
	}
}

// removeTextbookAssets deletes a removed textbook's PDF/cover from object
// storage when no other row references the content-addressed key. It is
// best-effort: the row is already gone, so failures are logged.
func (a *App) removeTextbookAssets(ctx context.Context, item sqlite.Textbook) {
	a.removeStoredFileIfUnused(ctx, item.PdfKey)
	a.removeCoverIfUnused(ctx, item.CoverKey)
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
	hash := md5Hex(data)
	key := publishedObjectKey("textbook", fileFormatPDF, hash)
	oldPdfKey := item.PdfKey
	oldCoverKey := item.CoverKey
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
	if oldPdfKey != "" && oldPdfKey != key {
		a.removeStoredFileIfUnused(ctx, oldPdfKey)
	}
	if oldCoverKey != "" {
		newCoverKey := textbookCoverKey(hash)
		if newCoverKey != oldCoverKey {
			if err := client.CopyObject(ctx, oldCoverKey, newCoverKey); err != nil {
				a.Logger.Warn("rekey textbook cover failed", "error", err, "textbook", item.ID)
			} else if err := a.Store.SetTextbookCoverKey(ctx, item.ID, newCoverKey, now); err != nil {
				a.Logger.Warn("update textbook cover key failed", "error", err, "textbook", item.ID)
			} else {
				item.CoverKey = newCoverKey
				a.purgeCDN(ctx, newCoverKey)
				a.removeCoverIfUnused(ctx, oldCoverKey)
			}
		}
	}
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
	hash := objectHashFromKey(item.PdfKey)
	if hash == "" {
		return TextbookView{}, Err("pdf_required", "请先上传教材 PDF", 400)
	}
	key := textbookCoverKey(hash)
	oldCoverKey := item.CoverKey
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
	if oldCoverKey != "" && oldCoverKey != key {
		a.removeCoverIfUnused(ctx, oldCoverKey)
	}
	_ = a.Store.AddAudit(ctx, actor, "textbook_cover", "textbook", item.ID, "{}", now)
	return textbookView(item), nil
}

// TextbookResourceURL returns a temporary signed CDN URL for a published
// textbook asset. kind is "pdf" or "cover".
func (a *App) TextbookResourceURL(ctx context.Context, id, kind string, ttl time.Duration) (TextbookResourceView, error) {
	return a.textbookResourceURL(ctx, id, kind, ttl, false)
}

// TextbookResourceURLForAdmin also serves archived textbooks in the console.
func (a *App) TextbookResourceURLForAdmin(ctx context.Context, id, kind string, ttl time.Duration) (TextbookResourceView, error) {
	return a.textbookResourceURL(ctx, id, kind, ttl, true)
}

func (a *App) textbookResourceURL(ctx context.Context, id, kind string, ttl time.Duration, allowArchived bool) (TextbookResourceView, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return TextbookResourceView{}, err
	}
	if !allowArchived && !item.ArchivedAt.IsZero() {
		return TextbookResourceView{}, ErrNotFound
	}
	var key string
	kind = strings.TrimSpace(kind)
	switch kind {
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
	if a.cfg().CDNDomain == "" || a.cfg().CDNAuthKey == "" {
		return TextbookResourceView{}, Err("cdn_unavailable", "CDN 未配置", 503)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = a.cfg().CDNURLTTL
	}
	url := cloud.CDNSignedURL(a.cfg().CDNDomain, a.cfg().CDNAuthKey, "/"+key, ttl)
	version := storedFileVersion(key)
	if kind == "cover" {
		// Covers can be overwritten at the same PDF-based storage key.
		version = storedFileVersion(key + ":" + configVersion(item.UpdatedAt))
	}
	return TextbookResourceView{
		URL:         url,
		FileVersion: version,
		ExpiresAt:   time.Now().Add(ttl).UTC().Format(time.RFC3339),
	}, nil
}
