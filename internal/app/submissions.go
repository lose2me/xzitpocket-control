package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"xzitpocket-control/internal/cloud"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

// ResourceSubmissionTicketInput describes a file the app is about to upload.
// The file itself goes straight to COS through a short-lived signed URL; the
// control server only sees the metadata and a short verification range.
type ResourceSubmissionTicketInput struct {
	Kind       string `json:"kind"`
	CourseCode string `json:"course_code"`
	Name       string `json:"name"`
	Tags       string `json:"tags"`
	TargetID   string `json:"target_id"`
	Format     string `json:"format"`
	MD5        string `json:"md5"`
	SizeBytes  int64  `json:"size_bytes"`
}

// ResourceUploadTicketView is the direct-upload authorization returned to the
// app. UploadURL is a presigned COS PUT URL for a content-addressed key under
// books/submissions or papers/submissions.
type ResourceUploadTicketView struct {
	ID        string `json:"id"`
	UploadURL string `json:"upload_url"`
	ExpiresAt string `json:"expires_at"`
}

// SubmissionView is the administrator-facing review queue row.
type SubmissionView struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	CourseCode        string `json:"course_code"`
	Name              string `json:"name"`
	Tags              string `json:"tags"`
	SizeBytes         int64  `json:"size_bytes"`
	Status            string `json:"status"`
	HasPdf            bool   `json:"has_pdf"`
	Format            string `json:"format"`
	SubmittedByUserID string `json:"submitted_by_user_id,omitempty"`
	SubmittedByName   string `json:"submitted_by_name"`
	StudentID         string `json:"student_id,omitempty"`
	TargetID          string `json:"target_id,omitempty"`
	CreatedAt         string `json:"created_at"`
}

// SubmissionURLView is a temporary, signed CDN URL for a submission PDF.
type SubmissionURLView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

const (
	maxSubmissionBytes           = 80 << 20
	maxPendingSubmissionsPerUser = 10
	maxUploadingPerUser          = 3
	submissionTicketTTL          = 30 * time.Minute
	submissionStaleAfter         = time.Hour
)

func normalizeSubmissionKind(kind string) (string, bool) {
	switch strings.TrimSpace(kind) {
	case "textbook":
		return "textbook", true
	case "resource":
		return "resource", true
	default:
		return "", false
	}
}

// normalizeSubmissionFormat accepts PDF for every submission and Word
// documents for exam papers. An empty format defaults to PDF for older apps.
func normalizeSubmissionFormat(format string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", fileFormatPDF:
		return fileFormatPDF, true
	case fileFormatDOCX:
		return fileFormatDOCX, true
	case fileFormatDOC:
		return fileFormatDOC, true
	default:
		return "", false
	}
}

func submissionView(item sqlite.Submission) SubmissionView {
	return SubmissionView{
		ID: item.ID, Kind: item.Kind, CourseCode: item.CourseCode, Name: item.Name, Tags: item.Tags,
		SizeBytes: item.SizeBytes, Status: item.Status, HasPdf: item.PdfKey != "",
		Format:            storedFileFormat(item.PdfKey),
		SubmittedByUserID: item.SubmittedByUserID, SubmittedByName: item.SubmittedByName,
		TargetID: item.TargetID, CreatedAt: item.CreatedAt.Format(time.RFC3339),
	}
}

// submissionIdentity returns the display name and the encrypted student ID
// that every upload is bound to. The plaintext ID never comes from the client.
func (a *App) submissionIdentity(ctx context.Context, p SessionPrincipal) (name, studentIDCiphertext string) {
	name = strings.TrimSpace(p.User.DisplayName)
	if identity, err := a.Store.GetIdentityByUser(ctx, p.User.ID, identityProvider); err == nil {
		if name == "" {
			name = strings.TrimSpace(identity.StudentAlias)
		}
		studentIDCiphertext = identity.StudentIDCiphertext
	}
	if name == "" {
		name = p.User.ID
	}
	return name, studentIDCiphertext
}

// submissionViewForAdmin decrypts the bound student ID for the review table.
func (a *App) submissionViewForAdmin(item sqlite.Submission) SubmissionView {
	view := submissionView(item)
	if item.StudentIDCiphertext != "" {
		if studentID, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, item.StudentIDCiphertext); err == nil {
			view.StudentID = studentID
		}
	}
	return view
}

// CreateSubmissionTicket validates metadata, reserves the COS object key and
// returns a presigned PUT URL. The queue row stays hidden until the app calls
// CompleteSubmission after the direct upload.
func (a *App) CreateSubmissionTicket(ctx context.Context, p SessionPrincipal, in ResourceSubmissionTicketInput) (ResourceUploadTicketView, error) {
	kind, ok := normalizeSubmissionKind(in.Kind)
	if !ok {
		return ResourceUploadTicketView{}, Err("invalid_kind", "资料类型无效", 400)
	}
	format, ok := normalizeSubmissionFormat(in.Format)
	if !ok {
		return ResourceUploadTicketView{}, Err("invalid_format", "文件类型无效", 400)
	}
	if kind == "textbook" && format != fileFormatPDF {
		return ResourceUploadTicketView{}, Err("textbook_pdf_only", "教材仅支持 PDF", 400)
	}
	hash, ok := normalizeMD5(in.MD5)
	if !ok {
		return ResourceUploadTicketView{}, Err("invalid_md5", "请更新 App 后重试", 400)
	}
	courseCode, err := normalizeCourseCode(in.CourseCode)
	if err != nil {
		return ResourceUploadTicketView{}, err
	}
	exists, err := a.Store.CourseCodeExists(ctx, courseCode)
	if err != nil {
		return ResourceUploadTicketView{}, err
	}
	if !exists {
		return ResourceUploadTicketView{}, Err("invalid_course_code", "该课程不存在", 400)
	}
	name := strings.TrimSpace(in.Name)
	tags := strings.TrimSpace(in.Tags)
	targetID := ""
	if kind == "textbook" {
		targetID = strings.TrimSpace(in.TargetID)
		if targetID == "" {
			return ResourceUploadTicketView{}, Err("textbook_required", "请选择教材", 400)
		}
		textbook, err := a.Store.GetTextbook(ctx, targetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ResourceUploadTicketView{}, Err("textbook_missing", "教材不存在", 400)
		}
		if err != nil {
			return ResourceUploadTicketView{}, err
		}
		associated := textbook.CourseCode == courseCode
		for _, code := range textbook.CourseCodes {
			if code == courseCode {
				associated = true
				break
			}
		}
		if !associated {
			return ResourceUploadTicketView{}, Err("textbook_course_mismatch", "教材不属于该课程", 400)
		}
		name = textbook.Name
		tags = textbook.Tags
	}
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "\r\n\x00") {
		return ResourceUploadTicketView{}, Err("invalid_name", "资料名称无效", 400)
	}
	if len(tags) > 1024 || strings.ContainsAny(tags, "\r\n\x00") {
		return ResourceUploadTicketView{}, Err("invalid_tags", "标签无效", 400)
	}
	if in.SizeBytes <= 0 || in.SizeBytes > maxSubmissionBytes {
		return ResourceUploadTicketView{}, Err("invalid_size", "文件大小无效", 400)
	}

	a.cleanupStaleUploads(ctx)
	pending, err := a.Store.CountSubmissionsByUser(ctx, p.User.ID)
	if err != nil {
		return ResourceUploadTicketView{}, err
	}
	if pending >= maxPendingSubmissionsPerUser {
		return ResourceUploadTicketView{}, Err("submission_limit", "待审核资料过多，请等待审核", 429)
	}
	uploading, err := a.Store.CountUploadingSubmissionsByUser(ctx, p.User.ID)
	if err != nil {
		return ResourceUploadTicketView{}, err
	}
	if uploading >= maxUploadingPerUser {
		return ResourceUploadTicketView{}, Err("upload_in_progress", "有正在上传的资料，请稍后再试", 429)
	}
	client, err := a.requireStorage()
	if err != nil {
		return ResourceUploadTicketView{}, err
	}

	key := submissionObjectKey(kind, format, hash)
	if _, err := client.HeadObject(ctx, key); err == nil {
		return ResourceUploadTicketView{}, Err("submission_duplicate", "该文件已提交过，请勿重复上传", 409)
	}

	now := time.Now().UTC()
	submitterName, studentIDCiphertext := a.submissionIdentity(ctx, p)
	item, err := a.Store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: kind, CourseCode: courseCode, Name: name, Tags: tags, SizeBytes: in.SizeBytes, TargetID: targetID,
		Status: "pending_upload", SubmittedByUserID: p.User.ID,
		SubmittedByName: submitterName, StudentIDCiphertext: studentIDCiphertext,
	}, now)
	if err != nil {
		return ResourceUploadTicketView{}, err
	}
	if err := a.Store.SetSubmissionPdfKey(ctx, item.ID, key, in.SizeBytes, now); err != nil {
		_ = a.Store.DeleteUploadingSubmission(ctx, item.ID)
		return ResourceUploadTicketView{}, err
	}
	uploadURL, err := client.PresignPutURL(key, in.SizeBytes, submissionTicketTTL)
	if err != nil {
		_ = a.Store.DeleteUploadingSubmission(ctx, item.ID)
		a.Logger.Error("presign submission upload failed", "error", err, "submission", item.ID)
		return ResourceUploadTicketView{}, Err("storage_error", "创建上传通道失败", 502)
	}
	return ResourceUploadTicketView{
		ID:        item.ID,
		UploadURL: uploadURL,
		ExpiresAt: time.Now().Add(submissionTicketTTL).UTC().Format(time.RFC3339),
	}, nil
}

// CompleteSubmission verifies the directly uploaded object and moves the
// ticket into the administrator review queue.
func (a *App) CompleteSubmission(ctx context.Context, p SessionPrincipal, id string) (SubmissionView, error) {
	item, err := a.Store.GetSubmission(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return SubmissionView{}, ErrNotFound
	}
	if err != nil {
		return SubmissionView{}, err
	}
	if item.Status != "pending_upload" || item.SubmittedByUserID != p.User.ID {
		return SubmissionView{}, ErrNotFound
	}
	if item.PdfKey == "" {
		a.discardUpload(ctx, item)
		return SubmissionView{}, Err("upload_missing", "上传信息无效", 409)
	}
	client, err := a.requireStorage()
	if err != nil {
		return SubmissionView{}, err
	}
	size, etag, err := client.HeadObjectInfo(ctx, item.PdfKey)
	if err != nil {
		return SubmissionView{}, Err("upload_missing", "未检测到上传的文件", 409)
	}
	if size <= 0 || size > maxSubmissionBytes {
		a.discardUpload(ctx, item)
		return SubmissionView{}, Err("invalid_file", "文件内容无效", 400)
	}
	if expected := objectHashFromKey(item.PdfKey); expected != "" && !strings.EqualFold(etag, expected) {
		a.discardUpload(ctx, item)
		return SubmissionView{}, Err("invalid_file", "文件校验失败，请重试", 400)
	}
	prefix, err := client.GetObjectRange(ctx, item.PdfKey, 0, 7)
	if err != nil || !storedFileIsValid(item.PdfKey, prefix) {
		a.discardUpload(ctx, item)
		return SubmissionView{}, Err("invalid_file", "文件内容无效", 400)
	}
	now := time.Now().UTC()
	if err := a.Store.MarkSubmissionUploaded(ctx, item.ID, size, now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubmissionView{}, ErrNotFound
		}
		return SubmissionView{}, err
	}
	item.Status = "pending_review"
	item.SizeBytes = size
	item.UpdatedAt = now
	_ = a.Store.AddAudit(ctx, p.User.ID, item.Kind+"_submission_create", item.Kind+"_submission", item.ID,
		controlcrypto.JSON(map[string]any{"course_code": item.CourseCode, "name": item.Name, "size_bytes": size}), now)
	return a.submissionViewForAdmin(item), nil
}

// AbortSubmission discards a direct-upload ticket after a failed client upload.
func (a *App) AbortSubmission(ctx context.Context, p SessionPrincipal, id string) error {
	item, err := a.Store.GetSubmission(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if item.Status != "pending_upload" || item.SubmittedByUserID != p.User.ID {
		return ErrNotFound
	}
	if err := a.Store.DeleteUploadingSubmission(ctx, item.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	a.removeUploadObject(ctx, item)
	return nil
}

// discardUpload removes verification-failed tickets and their objects.
func (a *App) discardUpload(ctx context.Context, item sqlite.Submission) {
	if err := a.Store.DeleteUploadingSubmission(ctx, item.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.Logger.Warn("delete rejected upload ticket failed", "error", err, "submission", item.ID)
	}
	a.removeUploadObject(ctx, item)
}

// removeUploadObject is best-effort and skips keys that approved content uses.
func (a *App) removeUploadObject(ctx context.Context, item sqlite.Submission) {
	if item.PdfKey == "" {
		return
	}
	inUse, err := a.Store.PdfKeyInUse(ctx, item.PdfKey)
	if err != nil {
		a.Logger.Warn("check upload object usage failed", "error", err, "key", item.PdfKey)
		return
	}
	if inUse {
		return
	}
	if client := a.cosClient(); client.Configured() {
		if err := client.DeleteObject(ctx, item.PdfKey); err != nil {
			a.Logger.Warn("delete upload object failed", "error", err, "key", item.PdfKey)
		}
	}
	a.purgeCDN(ctx, item.PdfKey)
}

// cleanupStaleUploads removes tickets whose client never completed the direct
// upload. It runs on ticket creation and is intentionally bounded.
func (a *App) cleanupStaleUploads(ctx context.Context) {
	// Keep ticket creation responsive even when object storage is slow.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	items, err := a.Store.ListStaleSubmissions(ctx, time.Now().Add(-submissionStaleAfter), 20)
	if err != nil {
		a.Logger.Warn("list stale upload tickets failed", "error", err)
		return
	}
	for _, item := range items {
		if err := a.Store.DeleteUploadingSubmission(ctx, item.ID); err != nil {
			continue
		}
		a.removeUploadObject(ctx, item)
	}
}

func (a *App) ListSubmissionsForAdmin(ctx context.Context, kind string, limit, offset int) ([]SubmissionView, int, error) {
	kind, ok := normalizeSubmissionKind(kind)
	if !ok {
		return nil, 0, Err("invalid_kind", "资料类型无效", 400)
	}
	items, err := a.Store.ListSubmissions(ctx, kind, clampLimit(limit), max0(offset))
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountSubmissions(ctx, kind)
	if err != nil {
		return nil, 0, err
	}
	views := make([]SubmissionView, 0, len(items))
	for _, item := range items {
		views = append(views, a.submissionViewForAdmin(item))
	}
	return views, total, nil
}

func (a *App) requireSubmission(ctx context.Context, id, kind string) (sqlite.Submission, error) {
	item, err := a.Store.GetSubmission(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return sqlite.Submission{}, ErrNotFound
	}
	if err != nil {
		return sqlite.Submission{}, err
	}
	if item.Kind != kind || item.Status != "pending_review" {
		return sqlite.Submission{}, ErrNotFound
	}
	return item, nil
}

// recordContribution links approved content to its contributor and increments
// the profile counters shown on the app upload page. Both steps are
// best-effort: the content has already been created.
func (a *App) recordContribution(ctx context.Context, userID string, link func(string) error) {
	if userID == "" {
		return
	}
	if err := link(userID); err != nil {
		a.Logger.Warn("link contribution failed", "error", err, "user", userID)
	}
	if err := a.Store.IncrementUserContribution(ctx, userID); err != nil {
		a.Logger.Warn("increment contribution count failed", "error", err, "user", userID)
	}
}

// ApproveSubmissionForAdmin creates the reviewed textbook or resource with the
// uploaded PDF and removes the queue row. A name conflict keeps the row so the
// reviewer can reject it or rename the existing content first.
func (a *App) ApproveSubmissionForAdmin(ctx context.Context, id, kind, actor string) (map[string]any, error) {
	kind, ok := normalizeSubmissionKind(kind)
	if !ok {
		return nil, Err("invalid_kind", "资料类型无效", 400)
	}
	item, err := a.requireSubmission(ctx, id, kind)
	if err != nil {
		return nil, err
	}
	if item.PdfKey == "" {
		return nil, Err("resource_missing", "该提交没有文件", 404)
	}
	format := storedFileFormat(item.PdfKey)
	if kind == "resource" && format != fileFormatPDF {
		return nil, Err("word_requires_pdf", "Word 试卷请转为 PDF 后再发布", 409)
	}
	hash := objectHashFromKey(item.PdfKey)
	publishedKey := item.PdfKey
	var client *cloud.COSClient
	if hash != "" {
		client, err = a.requireStorage()
		if err != nil {
			return nil, err
		}
		achieved := achievedObjectKey(kind, format, hash)
		if achieved != item.PdfKey {
			if err := client.CopyObject(ctx, item.PdfKey, achieved); err != nil {
				a.Logger.Error("copy submission to achieved failed", "error", err, "submission", item.ID)
				return nil, Err("storage_error", "发布文件失败，请稍后重试", 502)
			}
			publishedKey = achieved
			a.purgeCDN(ctx, achieved)
		}
	}
	now := time.Now().UTC()
	var result map[string]any
	switch kind {
	case "textbook":
		var textbook sqlite.Textbook
		created := false
		if item.TargetID != "" {
			existing, err := a.Store.GetTextbook(ctx, item.TargetID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, Err("textbook_missing", "教材不存在，请驳回该提交", 409)
			}
			if err != nil {
				return nil, err
			}
			textbook = existing
		} else {
			createdTextbook, err := a.Store.CreateTextbook(ctx, sqlite.TextbookInput{
				Name: item.Name, Tags: item.Tags, CourseCode: item.CourseCode, CourseCodes: []string{item.CourseCode},
			}, now)
			if errors.Is(err, sqlite.ErrTextbookExists) {
				return nil, Err("textbook_exists", "同名教材已存在，请驳回该提交", 409)
			}
			if err != nil {
				return nil, err
			}
			textbook = createdTextbook
			created = true
		}
		oldPdfKey := textbook.PdfKey
		oldCoverKey := textbook.CoverKey
		if err := a.Store.SetTextbookPdfKey(ctx, textbook.ID, publishedKey, now); err != nil {
			if created {
				_ = a.Store.DeleteTextbook(ctx, textbook.ID)
			}
			return nil, err
		}
		if item.SubmittedByUserID != "" {
			// The latest PDF contributor owns future download attribution.
			oldContributor := textbook.ContributorUserID
			if err := a.Store.SetTextbookContributor(ctx, textbook.ID, item.SubmittedByUserID); err != nil {
				a.Logger.Warn("link contribution failed", "error", err, "textbook", textbook.ID)
			} else {
				if oldContributor != item.SubmittedByUserID {
					a.transferContribution(ctx, oldContributor, item.SubmittedByUserID)
				}
				textbook.ContributorUserID = item.SubmittedByUserID
			}
		}
		if oldPdfKey != "" && oldPdfKey != publishedKey {
			a.removeStoredFileIfUnused(ctx, oldPdfKey)
		}
		textbook.PdfKey = publishedKey
		if hash != "" && oldCoverKey != "" {
			newCoverKey := textbookCoverKey(hash)
			if newCoverKey != oldCoverKey {
				if err := client.CopyObject(ctx, oldCoverKey, newCoverKey); err != nil {
					a.Logger.Warn("rekey textbook cover failed", "error", err, "textbook", textbook.ID)
				} else if err := a.Store.SetTextbookCoverKey(ctx, textbook.ID, newCoverKey, now); err != nil {
					a.Logger.Warn("update textbook cover key failed", "error", err, "textbook", textbook.ID)
				} else {
					textbook.CoverKey = newCoverKey
					a.purgeCDN(ctx, newCoverKey)
					a.removeCoverIfUnused(ctx, oldCoverKey)
				}
			}
		}
		textbook.UpdatedAt = now
		view := textbookView(textbook)
		view.SizeBytes = item.SizeBytes
		result = map[string]any{"kind": kind, "textbook": view}
		_ = a.Store.AddAudit(ctx, actor, "textbook_submission_approve", "textbook_submission", item.ID,
			controlcrypto.JSON(map[string]any{"textbook_id": textbook.ID, "targeted": item.TargetID != ""}), now)
	case "resource":
		resource, err := a.Store.CreateResource(ctx, sqlite.ResourceInput{
			Name: item.Name, Tags: item.Tags, CourseCode: item.CourseCode,
		}, now)
		if errors.Is(err, sqlite.ErrResourceExists) {
			return nil, Err("resource_exists", "该课程下已有同名试卷，请驳回该提交", 409)
		}
		if err != nil {
			return nil, err
		}
		if err := a.Store.SetResourcePdfKey(ctx, resource.ID, publishedKey, now); err != nil {
			_ = a.Store.DeleteResource(ctx, resource.ID)
			return nil, err
		}
		a.recordContribution(ctx, item.SubmittedByUserID, func(userID string) error {
			return a.Store.SetResourceContributor(ctx, resource.ID, userID)
		})
		resource.PdfKey = publishedKey
		resource.UpdatedAt = now
		view := resourceView(resource)
		view.SizeBytes = item.SizeBytes
		result = map[string]any{"kind": kind, "resource": view}
		_ = a.Store.AddAudit(ctx, actor, "resource_submission_approve", "resource_submission", item.ID,
			controlcrypto.JSON(map[string]any{"resource_id": resource.ID}), now)
	}
	if err := a.Store.DeleteSubmission(ctx, item.ID); err != nil {
		// The reviewed content already exists. Leave the queue row in place for
		// an operator instead of failing a request that has taken effect.
		a.Logger.Warn("delete reviewed submission failed", "error", err, "submission", item.ID)
	} else if publishedKey != item.PdfKey {
		a.removeStoredFileIfUnused(ctx, item.PdfKey)
	}
	return result, nil
}

// RejectSubmissionForAdmin removes the upload. Its object is deleted unless an
// approved content row already references the same key.
func (a *App) RejectSubmissionForAdmin(ctx context.Context, id, kind, actor string) error {
	kind, ok := normalizeSubmissionKind(kind)
	if !ok {
		return Err("invalid_kind", "资料类型无效", 400)
	}
	item, err := a.requireSubmission(ctx, id, kind)
	if err != nil {
		return err
	}
	if err := a.Store.DeleteSubmission(ctx, item.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	a.removeSubmissionAssets(ctx, item)
	return a.Store.AddAudit(ctx, actor, kind+"_submission_reject", kind+"_submission", item.ID, "{}", time.Now().UTC())
}

// removeSubmissionAssets is best-effort: the row is already gone. An object
// still referenced by approved content is kept.
func (a *App) removeSubmissionAssets(ctx context.Context, item sqlite.Submission) {
	a.removeUploadObject(ctx, item)
}

// SubmissionResourceURL returns a temporary signed URL so a reviewer can open
// the uploaded PDF before approving it.
func (a *App) SubmissionResourceURL(ctx context.Context, id, kind string, ttl time.Duration) (SubmissionURLView, error) {
	kind, ok := normalizeSubmissionKind(kind)
	if !ok {
		return SubmissionURLView{}, Err("invalid_kind", "资料类型无效", 400)
	}
	item, err := a.requireSubmission(ctx, id, kind)
	if err != nil {
		return SubmissionURLView{}, err
	}
	if item.PdfKey == "" {
		return SubmissionURLView{}, Err("resource_missing", "该提交没有文件", 404)
	}
	if a.Cfg.CDNDomain == "" || a.Cfg.CDNAuthKey == "" {
		return SubmissionURLView{}, Err("cdn_unavailable", "CDN 未配置", 503)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = a.Cfg.CDNURLTTL
	}
	url := cloud.CDNSignedURL(a.Cfg.CDNDomain, a.Cfg.CDNAuthKey, "/"+item.PdfKey, ttl)
	return SubmissionURLView{
		URL:       url,
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
	}, nil
}
