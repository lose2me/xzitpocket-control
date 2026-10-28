package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
)

// RequireDownloadContribution enforces the console setting that gates app
// downloads from the resource centre. Contributed content is counted from the
// published textbooks and exam papers credited to the user, so a pending
// upload does not unlock downloads. A zero threshold disables the check.
func (a *App) RequireDownloadContribution(ctx context.Context, userID string) error {
	required := a.cfg().DownloadMinContributions
	if required <= 0 {
		return nil
	}
	count, err := a.Store.CountContributions(ctx, userID)
	if err != nil {
		return err
	}
	if count < required {
		return Err(
			"contribution_required",
			fmt.Sprintf("至少贡献 %d 个资源后才能下载", required),
			http.StatusForbidden,
		)
	}
	return nil
}

// OpenTextbookDownload streams a textbook for the console. Admin downloads do
// not count toward the popularity numbers. Callers must close resp.Body.
func (a *App) OpenTextbookDownload(ctx context.Context, id string) (*http.Response, string, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if item.PdfKey == "" {
		return nil, "", Err("resource_missing", "该教材没有文件", 404)
	}
	resp, err := a.openStoredFile(ctx, item.PdfKey)
	if err != nil {
		return nil, "", err
	}
	return resp, downloadFileName(item.Name, storedFileFormat(item.PdfKey)), nil
}

// OpenResourceDownload streams a paper for the console. Admin downloads do not
// count toward the popularity numbers.
func (a *App) OpenResourceDownload(ctx context.Context, id string) (*http.Response, string, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if item.PdfKey == "" {
		return nil, "", Err("resource_missing", "该试卷没有文件", 404)
	}
	resp, err := a.openStoredFile(ctx, item.PdfKey)
	if err != nil {
		return nil, "", err
	}
	return resp, downloadFileName(item.Name, storedFileFormat(item.PdfKey)), nil
}

// OpenSubmissionDownload streams one file of a pending upload for review. It
// is not counted as a textbook/exam download because the submission is not
// published. fileID identifies the file inside the submission.
func (a *App) OpenSubmissionDownload(ctx context.Context, id, kind, fileID string) (*http.Response, string, error) {
	kind, ok := normalizeSubmissionKind(kind)
	if !ok {
		return nil, "", Err("invalid_kind", "资料类型无效", 400)
	}
	item, err := a.requireSubmission(ctx, id, kind)
	if err != nil {
		return nil, "", err
	}
	if len(item.Files) == 0 {
		return nil, "", Err("resource_missing", "该提交没有文件", 404)
	}
	file := item.Files[0]
	if strings.TrimSpace(fileID) != "" {
		found, err := a.Store.GetSubmissionFile(ctx, strings.TrimSpace(fileID))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && found.SubmissionID != item.ID) {
			return nil, "", ErrNotFound
		}
		if err != nil {
			return nil, "", err
		}
		file = found
	}
	resp, err := a.openStoredFile(ctx, file.ObjectKey)
	if err != nil {
		return nil, "", err
	}
	return resp, downloadFileName(file.Name, storedFileFormat(file.ObjectKey)), nil
}

func (a *App) openStoredFile(ctx context.Context, key string) (*http.Response, error) {
	client, err := a.requireStorage()
	if err != nil {
		return nil, err
	}
	resp, err := client.GetObject(ctx, key)
	if err != nil {
		a.Logger.Error("open stored file failed", "error", err, "key", key)
		return nil, Err("storage_error", "下载文件失败", 502)
	}
	return resp, nil
}

// removeStoredFileIfUnused deletes a content-addressed object only after no
// textbook, resource or pending submission still references it.
func (a *App) removeStoredFileIfUnused(ctx context.Context, key string) {
	if key == "" {
		return
	}
	inUse, err := a.Store.PdfKeyInUse(ctx, key)
	if err != nil {
		a.Logger.Warn("check stored file usage failed", "error", err, "key", key)
		return
	}
	if inUse {
		return
	}
	if client := a.cosClient(); client.Configured() {
		if err := client.DeleteObject(ctx, key); err != nil {
			a.Logger.Warn("delete stored file failed", "error", err, "key", key)
			return
		}
	}
	a.purgeCDN(ctx, key)
}

// removeCoverIfUnused deletes a textbook cover only when no other textbook
// still points at the same key.
func (a *App) removeCoverIfUnused(ctx context.Context, key string) {
	if key == "" {
		return
	}
	inUse, err := a.Store.CoverKeyInUse(ctx, key)
	if err != nil {
		a.Logger.Warn("check cover usage failed", "error", err, "key", key)
		return
	}
	if inUse {
		return
	}
	if client := a.cosClient(); client.Configured() {
		if err := client.DeleteObject(ctx, key); err != nil {
			a.Logger.Warn("delete cover failed", "error", err, "key", key)
			return
		}
	}
	a.purgeCDN(ctx, key)
}

// UploadStatsView is the app upload page summary. StudentID is the account
// every upload is bound to.
type UploadStatsView struct {
	ContributionCount int64  `json:"contribution_count"`
	DownloadCount     int64  `json:"download_count"`
	StudentID         string `json:"student_id"`
}

// GetUploadStats returns the signed-in user's contribution and download
// counters plus the bound student ID.
func (a *App) GetUploadStats(ctx context.Context, p SessionPrincipal) (UploadStatsView, error) {
	contribution, downloads, err := a.Store.GetUserStats(ctx, p.User.ID)
	if err != nil {
		return UploadStatsView{}, err
	}
	view := UploadStatsView{ContributionCount: contribution, DownloadCount: downloads}
	if identity, err := a.Store.GetIdentityByUser(ctx, p.User.ID, identityProvider); err == nil {
		if studentID, err := controlcrypto.Decrypt(a.cfg().EncryptionKey, identity.StudentIDCiphertext); err == nil {
			view.StudentID = studentID
		}
	}
	return view, nil
}

// RecordTextbookDownload counts one app download per user and day, so retries
// and repeated taps do not inflate the numbers. Admin downloads never come
// through here. Best-effort: a failed counter update must not break the
// download.
func (a *App) RecordTextbookDownload(ctx context.Context, id, userID string) {
	counted, err := a.Store.MarkDownloadOnce(ctx, "textbook", id, userID, time.Now().UTC())
	if err != nil {
		a.Logger.Warn("mark textbook download failed", "error", err, "textbook", id)
		return
	}
	if !counted {
		return
	}
	if err := a.Store.IncrementTextbookDownloads(ctx, id); err != nil {
		a.Logger.Warn("record textbook download failed", "error", err, "textbook", id)
		return
	}
	item, err := a.Store.GetTextbook(ctx, id)
	if err != nil || item.ContributorUserID == "" {
		return
	}
	if err := a.Store.IncrementUserDownloads(ctx, item.ContributorUserID); err != nil {
		a.Logger.Warn("increment contributor downloads failed", "error", err, "user", item.ContributorUserID)
	}
}

// RecordResourceDownload counts one app download per user and day; see
// RecordTextbookDownload.
func (a *App) RecordResourceDownload(ctx context.Context, id, userID string) {
	counted, err := a.Store.MarkDownloadOnce(ctx, "resource", id, userID, time.Now().UTC())
	if err != nil {
		a.Logger.Warn("mark resource download failed", "error", err, "resource", id)
		return
	}
	if !counted {
		return
	}
	if err := a.Store.IncrementResourceDownloads(ctx, id); err != nil {
		a.Logger.Warn("record resource download failed", "error", err, "resource", id)
		return
	}
	item, err := a.Store.GetResource(ctx, id)
	if err != nil || item.ContributorUserID == "" {
		return
	}
	if err := a.Store.IncrementUserDownloads(ctx, item.ContributorUserID); err != nil {
		a.Logger.Warn("increment contributor downloads failed", "error", err, "user", item.ContributorUserID)
	}
}
