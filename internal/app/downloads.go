package app

import (
	"context"
	"net/http"

	controlcrypto "xzitpocket-control/internal/crypto"
)

// OpenTextbookDownload counts the download and returns the stored file stream
// plus the attachment filename. Callers must close resp.Body.
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
	a.RecordTextbookDownload(ctx, item.ID, true)
	return resp, downloadFileName(item.Name, storedFileFormat(item.PdfKey)), nil
}

// OpenResourceDownload counts the download and returns the stored file stream.
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
	a.RecordResourceDownload(ctx, item.ID, true)
	return resp, downloadFileName(item.Name, storedFileFormat(item.PdfKey)), nil
}

// OpenSubmissionDownload streams a pending upload for review. It is not
// counted as a textbook/exam download because the submission is not published.
func (a *App) OpenSubmissionDownload(ctx context.Context, id, kind string) (*http.Response, string, error) {
	kind, ok := normalizeSubmissionKind(kind)
	if !ok {
		return nil, "", Err("invalid_kind", "资料类型无效", 400)
	}
	item, err := a.requireSubmission(ctx, id, kind)
	if err != nil {
		return nil, "", err
	}
	if item.PdfKey == "" {
		return nil, "", Err("resource_missing", "该提交没有文件", 404)
	}
	resp, err := a.openStoredFile(ctx, item.PdfKey)
	if err != nil {
		return nil, "", err
	}
	return resp, downloadFileName(item.Name, storedFileFormat(item.PdfKey)), nil
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
		if studentID, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, identity.StudentIDCiphertext); err == nil {
			view.StudentID = studentID
		}
	}
	return view, nil
}

// RecordTextbookDownload is best-effort: a failed counter update must not
// break the download. Every counted download, app or admin, also counts toward
// the contributor's profile.
func (a *App) RecordTextbookDownload(ctx context.Context, id string, includeContributor bool) {
	if err := a.Store.IncrementTextbookDownloads(ctx, id); err != nil {
		a.Logger.Warn("record textbook download failed", "error", err, "textbook", id)
		return
	}
	if !includeContributor {
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

// RecordResourceDownload is best-effort: a failed counter update must not
// break the download. Every counted download, app or admin, also counts toward
// the contributor's profile.
func (a *App) RecordResourceDownload(ctx context.Context, id string, includeContributor bool) {
	if err := a.Store.IncrementResourceDownloads(ctx, id); err != nil {
		a.Logger.Warn("record resource download failed", "error", err, "resource", id)
		return
	}
	if !includeContributor {
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
