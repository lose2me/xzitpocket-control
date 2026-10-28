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
	Kind       string                        `json:"kind"`
	CourseCode string                        `json:"course_code"`
	Name       string                        `json:"name"`
	Tags       string                        `json:"tags"`
	Note       string                        `json:"note"`
	TargetID   string                        `json:"target_id"`
	Files      []ResourceSubmissionFileInput `json:"files"`
}

// ResourceSubmissionFileInput describes one file of a submit. The app sends all
// files of a submit together so the administrator reviews them as one record.
type ResourceSubmissionFileInput struct {
	Name      string `json:"name"`
	Format    string `json:"format"`
	MD5       string `json:"md5"`
	SizeBytes int64  `json:"size_bytes"`
}

// ResourceUploadTicketView is the direct-upload authorization returned to the
// app. Every file gets its own presigned COS PUT URL for a content-addressed
// key under books/submissions or papers/submissions.
type ResourceUploadTicketView struct {
	ID        string                   `json:"id"`
	Files     []ResourceUploadFileView `json:"files"`
	ExpiresAt string                   `json:"expires_at"`
}

type ResourceUploadFileView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UploadURL string `json:"upload_url"`
}

// SubmissionView is the administrator-facing review queue row.
type SubmissionView struct {
	ID                string               `json:"id"`
	Kind              string               `json:"kind"`
	CourseCode        string               `json:"course_code"`
	Name              string               `json:"name"`
	Tags              string               `json:"tags"`
	Note              string               `json:"note"`
	SizeBytes         int64                `json:"size_bytes"`
	Status            string               `json:"status"`
	SubmittedByUserID string               `json:"submitted_by_user_id,omitempty"`
	SubmittedByName   string               `json:"submitted_by_name"`
	StudentID         string               `json:"student_id,omitempty"`
	TargetID          string               `json:"target_id,omitempty"`
	CreatedAt         string               `json:"created_at"`
	Files             []SubmissionFileView `json:"files"`
}

// SubmissionFileView is one file of a submission in the review queue.
type SubmissionFileView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
	Duplicate bool   `json:"duplicate"`
}

// SubmissionURLView is a temporary, signed CDN URL for a submission PDF.
type SubmissionURLView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

const (
	maxSubmissionBytes           = 80 << 20
	maxSubmissionFiles           = 10
	maxSubmissionNote            = 500
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
// documents for exam papers.
func normalizeSubmissionFormat(format string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case fileFormatPDF:
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
	files := make([]SubmissionFileView, 0, len(item.Files))
	for _, file := range item.Files {
		files = append(files, SubmissionFileView{
			ID: file.ID, Name: file.Name, Format: file.Format,
			SizeBytes: file.SizeBytes, Duplicate: file.Duplicate,
		})
	}
	return SubmissionView{
		ID: item.ID, Kind: item.Kind, CourseCode: item.CourseCode, Name: item.Name, Tags: item.Tags, Note: item.Note,
		SizeBytes: item.SizeBytes, Status: item.Status,
		SubmittedByUserID: item.SubmittedByUserID, SubmittedByName: item.SubmittedByName,
		TargetID: item.TargetID, CreatedAt: item.CreatedAt.Format(time.RFC3339),
		Files: files,
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
		if studentID, err := controlcrypto.Decrypt(a.cfg().EncryptionKey, item.StudentIDCiphertext); err == nil {
			view.StudentID = studentID
		}
	}
	return view
}

// CreateSubmissionTicket validates metadata, reserves one COS object key per
// file and returns presigned PUT URLs. One submit is one review record, so all
// files of a submit share the ticket. The queue row stays hidden until the app
// calls CompleteSubmission.
func (a *App) CreateSubmissionTicket(ctx context.Context, p SessionPrincipal, in ResourceSubmissionTicketInput) (ResourceUploadTicketView, error) {
	kind, ok := normalizeSubmissionKind(in.Kind)
	if !ok {
		return ResourceUploadTicketView{}, Err("invalid_kind", "资料类型无效", 400)
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
	if !a.cfg().IngestSubmissions.Enabled() {
		return ResourceUploadTicketView{}, Err("ingest_disabled", "上传已关闭，请稍后再试", 503)
	}
	if len(in.Files) == 0 || len(in.Files) > maxSubmissionFiles {
		return ResourceUploadTicketView{}, Err("invalid_files", "请选择 1-10 个文件", 400)
	}
	name := strings.TrimSpace(in.Name)
	tags := strings.TrimSpace(in.Tags)
	note := strings.TrimSpace(in.Note)
	if len(note) > maxSubmissionNote {
		return ResourceUploadTicketView{}, Err("invalid_note", "备注无效", 400)
	}
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
		associated := false
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

	type preparedFile struct {
		name         string
		key          string
		publishedKey string
		format       string
		sizeBytes    int64
		duplicate    bool
	}
	prepared := make([]preparedFile, 0, len(in.Files))
	for _, file := range in.Files {
		fileName := strings.TrimSpace(file.Name)
		if fileName == "" || len(fileName) > 256 || strings.ContainsAny(fileName, "\r\n\x00") {
			return ResourceUploadTicketView{}, Err("invalid_name", "文件名称无效", 400)
		}
		format, ok := normalizeSubmissionFormat(file.Format)
		if !ok {
			return ResourceUploadTicketView{}, Err("invalid_format", "文件类型无效", 400)
		}
		if kind == "textbook" && format != fileFormatPDF {
			return ResourceUploadTicketView{}, Err("textbook_pdf_only", "教材仅支持 PDF", 400)
		}
		hash, ok := normalizeMD5(file.MD5)
		if !ok {
			return ResourceUploadTicketView{}, Err("invalid_md5", "请更新 App 后重试", 400)
		}
		if file.SizeBytes <= 0 || file.SizeBytes > maxSubmissionBytes {
			return ResourceUploadTicketView{}, Err("invalid_size", "文件大小无效", 400)
		}
		key := submissionObjectKey(kind, format, hash)
		prepared = append(prepared, preparedFile{
			name: fileName, key: key, publishedKey: publishedObjectKey(kind, format, hash),
			format: format, sizeBytes: file.SizeBytes,
		})
	}
	if name == "" {
		name = prepared[0].name
	}
	if len(name) > 256 || strings.ContainsAny(name, "\r\n\x00") {
		return ResourceUploadTicketView{}, Err("invalid_name", "资料名称无效", 400)
	}
	if len(tags) > 1024 || strings.ContainsAny(tags, "\r\n\x00") {
		return ResourceUploadTicketView{}, Err("invalid_tags", "标签无效", 400)
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
	// Content that is already published is not rejected; the review queue marks
	// it so the administrator can see the paper/textbook is already on the site.
	for i := range prepared {
		if _, headErr := client.HeadObject(ctx, prepared[i].publishedKey); headErr == nil {
			prepared[i].duplicate = true
		}
	}

	now := time.Now().UTC()
	submitterName, studentIDCiphertext := a.submissionIdentity(ctx, p)
	item, err := a.Store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: kind, CourseCode: courseCode, Name: name, Tags: tags, Note: note, TargetID: targetID,
		Status: "pending_upload", SubmittedByUserID: p.User.ID,
		SubmittedByName: submitterName, StudentIDCiphertext: studentIDCiphertext,
	}, now)
	if err != nil {
		return ResourceUploadTicketView{}, err
	}
	uploadFiles := make([]ResourceUploadFileView, 0, len(prepared))
	for _, file := range prepared {
		row, err := a.Store.AddSubmissionFile(ctx, sqlite.SubmissionFileInput{
			SubmissionID: item.ID, Name: file.name, ObjectKey: file.key,
			Format: file.format, SizeBytes: file.sizeBytes, Duplicate: file.duplicate,
		}, now)
		if err != nil {
			_ = a.Store.DeleteSubmission(ctx, item.ID)
			return ResourceUploadTicketView{}, err
		}
		uploadURL, err := client.PresignPutURL(file.key, file.sizeBytes, submissionTicketTTL)
		if err != nil {
			_ = a.Store.DeleteSubmission(ctx, item.ID)
			a.Logger.Error("presign submission upload failed", "error", err, "submission", item.ID)
			return ResourceUploadTicketView{}, Err("storage_error", "创建上传通道失败", 502)
		}
		uploadFiles = append(uploadFiles, ResourceUploadFileView{ID: row.ID, Name: row.Name, UploadURL: uploadURL})
	}
	return ResourceUploadTicketView{
		ID:        item.ID,
		Files:     uploadFiles,
		ExpiresAt: time.Now().Add(submissionTicketTTL).UTC().Format(time.RFC3339),
	}, nil
}

// CompleteSubmission verifies every uploaded object and moves the ticket into
// the administrator review queue as one record with all of its files.
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
	if len(item.Files) == 0 {
		a.discardUpload(ctx, item)
		return SubmissionView{}, Err("upload_missing", "上传信息无效", 409)
	}
	client, err := a.requireStorage()
	if err != nil {
		return SubmissionView{}, err
	}
	total := int64(0)
	for _, file := range item.Files {
		if file.ObjectKey == "" {
			a.discardUpload(ctx, item)
			return SubmissionView{}, Err("upload_missing", "上传信息无效", 409)
		}
		size, etag, err := client.HeadObjectInfo(ctx, file.ObjectKey)
		if err != nil {
			a.discardUpload(ctx, item)
			return SubmissionView{}, Err("upload_missing", "未检测到上传的文件", 409)
		}
		if size <= 0 || size > maxSubmissionBytes {
			a.discardUpload(ctx, item)
			return SubmissionView{}, Err("invalid_file", "文件内容无效", 400)
		}
		if expected := objectHashFromKey(file.ObjectKey); expected != "" && !strings.EqualFold(etag, expected) {
			a.discardUpload(ctx, item)
			return SubmissionView{}, Err("invalid_file", "文件校验失败，请重试", 400)
		}
		prefix, err := client.GetObjectRange(ctx, file.ObjectKey, 0, 7)
		if err != nil || !storedFileIsValid(file.ObjectKey, prefix) {
			a.discardUpload(ctx, item)
			return SubmissionView{}, Err("invalid_file", "文件内容无效", 400)
		}
		if err := a.Store.UpdateSubmissionFileSize(ctx, file.ID, size); err != nil {
			return SubmissionView{}, err
		}
		total += size
	}
	now := time.Now().UTC()
	if err := a.Store.MarkSubmissionUploaded(ctx, item.ID, total, now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubmissionView{}, ErrNotFound
		}
		return SubmissionView{}, err
	}
	stored, err := a.Store.GetSubmission(ctx, item.ID)
	if err != nil {
		return SubmissionView{}, err
	}
	_ = a.Store.AddAudit(ctx, p.User.ID, item.Kind+"_submission_create", item.Kind+"_submission", item.ID,
		controlcrypto.JSON(map[string]any{"course_code": item.CourseCode, "name": item.Name, "size_bytes": total}), now)
	return a.submissionViewForAdmin(stored), nil
}

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
	a.removeSubmissionObjects(ctx, item)
	return nil
}

// discardUpload removes verification-failed tickets and their objects.
func (a *App) discardUpload(ctx context.Context, item sqlite.Submission) {
	if err := a.Store.DeleteUploadingSubmission(ctx, item.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.Logger.Warn("delete rejected upload ticket failed", "error", err, "submission", item.ID)
	}
	a.removeSubmissionObjects(ctx, item)
}

// removeSubmissionObjects deletes the objects of a discarded ticket, skipping
// keys another record or published content still points at.
func (a *App) removeSubmissionObjects(ctx context.Context, item sqlite.Submission) {
	keys := make([]string, 0, len(item.Files))
	for _, file := range item.Files {
		if file.ObjectKey != "" {
			keys = append(keys, file.ObjectKey)
		}
	}
	for _, key := range keys {
		a.removeSubmissionObjectIfUnused(ctx, key)
	}
}

// removeSubmissionObjectIfUnused deletes one submission object unless any other
// submission file, resource or textbook still references the same content.
func (a *App) removeSubmissionObjectIfUnused(ctx context.Context, key string) {
	if strings.TrimSpace(key) == "" {
		return
	}
	refs, err := a.Store.CountSubmissionFileKeyRefs(ctx, key)
	if err != nil {
		a.Logger.Warn("count submission object refs failed", "error", err, "key", key)
		return
	}
	if refs > 0 {
		return
	}
	inUse, err := a.Store.PdfKeyInUse(ctx, key)
	if err != nil {
		a.Logger.Warn("check upload object usage failed", "error", err, "key", key)
		return
	}
	if inUse {
		return
	}
	if client := a.cosClient(); client.Configured() {
		if err := client.DeleteObject(ctx, key); err != nil {
			a.Logger.Warn("delete upload object failed", "error", err, "key", key)
		}
	}
	a.purgeCDN(ctx, key)
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
		a.removeSubmissionObjects(ctx, item)
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

// DeleteSubmissionFileForAdmin removes one file of a review record, keeps the
// object when another record or published content shares it, and deletes the
// record itself once its last file is gone.
func (a *App) DeleteSubmissionFileForAdmin(ctx context.Context, id, kind, fileID, actor string) error {
	kind, ok := normalizeSubmissionKind(kind)
	if !ok {
		return Err("invalid_kind", "资料类型无效", 400)
	}
	item, err := a.requireSubmission(ctx, id, kind)
	if err != nil {
		return err
	}
	file, err := a.Store.GetSubmissionFile(ctx, strings.TrimSpace(fileID))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && file.SubmissionID != item.ID) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := a.Store.DeleteSubmissionFile(ctx, file.ID); err != nil {
		return err
	}
	a.removeSubmissionObjectIfUnused(ctx, file.ObjectKey)
	remaining, err := a.Store.CountSubmissionFiles(ctx, item.ID)
	if err != nil {
		return err
	}
	if remaining == 0 {
		if err := a.Store.DeleteSubmission(ctx, item.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return a.Store.AddAudit(ctx, actor, kind+"_submission_file_delete", kind+"_submission", item.ID,
		controlcrypto.JSON(map[string]any{"file": file.ID, "name": file.Name}), time.Now().UTC())
}

// RejectSubmissionForAdmin deletes a whole review record and its objects.
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
	a.removeSubmissionObjects(ctx, item)
	return a.Store.AddAudit(ctx, actor, kind+"_submission_reject", kind+"_submission", item.ID, "{}", time.Now().UTC())
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
	if len(item.Files) == 0 {
		return SubmissionURLView{}, Err("resource_missing", "该提交没有文件", 404)
	}
	if a.cfg().CDNDomain == "" || a.cfg().CDNAuthKey == "" {
		return SubmissionURLView{}, Err("cdn_unavailable", "CDN 未配置", 503)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = a.cfg().CDNURLTTL
	}
	url := cloud.CDNSignedURL(a.cfg().CDNDomain, a.cfg().CDNAuthKey, "/"+item.Files[0].ObjectKey, ttl)
	return SubmissionURLView{
		URL:       url,
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
	}, nil
}
