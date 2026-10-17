package app

import (
	"context"
	"net/http"
	"strings"

	controlcrypto "xzitpocket-control/internal/crypto"
)

// OpenTextbookDownload counts the download and returns the stored PDF stream
// plus the attachment filename. Callers must close resp.Body.
func (a *App) OpenTextbookDownload(ctx context.Context, id string) (*http.Response, string, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if item.PdfKey == "" {
		return nil, "", Err("resource_missing", "该教材没有 PDF", 404)
	}
	resp, err := a.openStoredPDF(ctx, item.PdfKey)
	if err != nil {
		return nil, "", err
	}
	a.RecordTextbookDownload(ctx, item.ID, false)
	return resp, pdfDownloadName(item.Name), nil
}

// OpenResourceDownload counts the download and returns the stored PDF stream.
func (a *App) OpenResourceDownload(ctx context.Context, id string) (*http.Response, string, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if item.PdfKey == "" {
		return nil, "", Err("resource_missing", "该试卷没有 PDF", 404)
	}
	resp, err := a.openStoredPDF(ctx, item.PdfKey)
	if err != nil {
		return nil, "", err
	}
	a.RecordResourceDownload(ctx, item.ID, false)
	return resp, pdfDownloadName(item.Name), nil
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
		return nil, "", Err("resource_missing", "该提交没有 PDF", 404)
	}
	resp, err := a.openStoredPDF(ctx, item.PdfKey)
	if err != nil {
		return nil, "", err
	}
	return resp, pdfDownloadName(item.Name), nil
}

func (a *App) openStoredPDF(ctx context.Context, key string) (*http.Response, error) {
	client, err := a.requireStorage()
	if err != nil {
		return nil, err
	}
	resp, err := client.GetObject(ctx, key)
	if err != nil {
		a.Logger.Error("open stored pdf failed", "error", err, "key", key)
		return nil, Err("storage_error", "下载 PDF 失败", 502)
	}
	return resp, nil
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
// break the download. App downloads also count toward the contributor's
// profile; admin downloads only update the textbook total.
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
// break the download. App downloads also count toward the contributor's
// profile; admin downloads only update the exam total.
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

func pdfDownloadName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case strings.ContainsRune(`/\:*?"<>|`, r):
			return '_'
		default:
			return r
		}
	}, name))
	if name == "" {
		name = "资料"
	}
	runes := []rune(name)
	if len(runes) > 180 {
		name = string(runes[:180])
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	return name
}
