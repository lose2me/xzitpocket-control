package app

import (
	"context"
	"net/http"
	"strings"
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
	a.RecordTextbookDownload(ctx, item.ID)
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
	a.RecordResourceDownload(ctx, item.ID)
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

// RecordTextbookDownload is best-effort: a failed counter update must not
// break the download.
func (a *App) RecordTextbookDownload(ctx context.Context, id string) {
	if err := a.Store.IncrementTextbookDownloads(ctx, id); err != nil {
		a.Logger.Warn("record textbook download failed", "error", err, "textbook", id)
	}
}

// RecordResourceDownload is best-effort: a failed counter update must not
// break the download.
func (a *App) RecordResourceDownload(ctx context.Context, id string) {
	if err := a.Store.IncrementResourceDownloads(ctx, id); err != nil {
		a.Logger.Warn("record resource download failed", "error", err, "resource", id)
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
