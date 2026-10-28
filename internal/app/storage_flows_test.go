package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/cloud"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

func putFailingCOS() *cloud.COSClient {
	client := cloud.NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: cosRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return cosStubResponse(http.StatusForbidden, "denied", 0), nil
	})}
	return client
}

func newStorageApp(t *testing.T) (*App, *sqlite.Store, context.Context) {
	t.Helper()
	a, store := newCourseApp(t)
	cfg := a.cfg()
	cfg.COSRegion = "ap-shanghai"
	cfg.COSBucket = "xzitpocket-assets-1330114136"
	cfg.COSSecretID = "id"
	cfg.COSSecretKey = "key"
	cfg.CDNDomain = ""
	cfg.CDNAuthKey = ""
	a.SetConfigForTesting(cfg)
	if _, err := a.CreateCourseForAdmin(context.Background(), CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	return a, store, context.Background()
}

func TestUploadCourseResourcesValidationAndFailures(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	a.cosClientOverride = newStubCOSClient(nil, nil)
	pdf := []byte("%PDF-1.4 hello")

	if _, err := a.UploadCourseResources(ctx, "", []ResourceUpload{{Name: "a.pdf", Data: pdf}}, "admin"); err == nil {
		t.Fatal("empty course code should fail")
	}
	if _, err := a.UploadCourseResources(ctx, "C1", nil, "admin"); err == nil {
		t.Fatal("no uploads should fail")
	}
	tooMany := make([]ResourceUpload, maxResourceUploads+1)
	for i := range tooMany {
		tooMany[i] = ResourceUpload{Name: "a.pdf", Data: pdf}
	}
	if _, err := a.UploadCourseResources(ctx, "C1", tooMany, "admin"); err == nil {
		t.Fatal("too many uploads should fail")
	}

	out, err := a.UploadCourseResources(ctx, "C1", []ResourceUpload{
		{Name: `C:\fakepath\高数 期末.pdf`, Data: pdf},
		{Name: "线性代数.pdf", Data: []byte("%PDF-1.4 second")},
		{Name: "坏文件.pdf", Data: []byte("not a pdf")},
		{Name: "", Data: nil},
	}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	items := out["items"].([]ResourceView)
	failed := out["failed"].([]map[string]string)
	if len(items) != 2 || len(failed) != 2 {
		t.Fatalf("upload = %#v", out)
	}
	if items[0].Name != "高数 期末.pdf" || !items[0].HasPdf {
		t.Fatalf("sanitized name = %#v", items[0])
	}
	if failed[0]["name"] != "坏文件.pdf" || failed[1]["name"] != "resource.pdf" {
		t.Fatalf("failed rows = %#v", failed)
	}

	// The same name in the same course is reported instead of duplicated.
	dup, err := a.UploadCourseResources(ctx, "C1", []ResourceUpload{{Name: "高数 期末.pdf", Data: pdf}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(dup["failed"].([]map[string]string)) != 1 || !strings.Contains(dup["failed"].([]map[string]string)[0]["reason"], "同名") {
		t.Fatalf("duplicate upload = %#v", dup)
	}

	// A storage failure removes the row instead of leaving a broken resource.
	a.cosClientOverride = putFailingCOS()
	out, err = a.UploadCourseResources(ctx, "C1", []ResourceUpload{{Name: "失败.pdf", Data: pdf}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(out["items"].([]ResourceView)) != 0 || len(out["failed"].([]map[string]string)) != 1 {
		t.Fatalf("storage failure = %#v", out)
	}
	resources, err := store.ListResources(ctx, 10, 0, "失败.pdf", "C1", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 0 {
		t.Fatal("failed upload should not leave a resource row")
	}
}

func TestUploadManualAnalysisFlows(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	a.cosClientOverride = newStubCOSClient(nil, nil)
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UploadManualAnalysis(ctx, resource.ID, []byte("nope"), "admin"); err == nil {
		t.Fatal("invalid pdf should fail")
	}
	if _, err := a.UploadManualAnalysis(ctx, "missing", []byte("%PDF-1.4"), "admin"); err == nil {
		t.Fatal("missing resource should fail")
	}
	view, err := a.UploadManualAnalysis(ctx, resource.ID, []byte("%PDF-1.4 manual"), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !view.HasManualAI {
		t.Fatalf("manual analysis = %#v", view)
	}
	stored, err := store.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstKey := stored.ManualAIKey
	if firstKey == "" || stored.ManualAISize == 0 {
		t.Fatalf("stored manual analysis = %#v", stored)
	}
	// A second upload replaces the previous object.
	view, err = a.UploadManualAnalysis(ctx, resource.ID, []byte("%PDF-1.4 manual v2"), "admin")
	if err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ManualAIKey == firstKey {
		t.Fatal("manual analysis should use a new content-addressed key")
	}
	a.cosClientOverride = putFailingCOS()
	if _, err := a.UploadManualAnalysis(ctx, resource.ID, []byte("%PDF-1.4 fail"), "admin"); err == nil {
		t.Fatal("storage failure should surface")
	}
}

func TestUploadTextbookAssets(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	a.cosClientOverride = newStubCOSClient(nil, nil)
	now := time.Now().UTC()
	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UploadTextbookPdf(ctx, textbook.ID, []byte("nope"), "admin"); err == nil {
		t.Fatal("invalid pdf should fail")
	}
	if _, err := a.UploadTextbookPdf(ctx, "missing", []byte("%PDF-1.4"), "admin"); err == nil {
		t.Fatal("missing textbook should fail")
	}
	if _, err := a.UploadTextbookCover(ctx, textbook.ID, []byte("cover"), "admin"); err == nil {
		t.Fatal("cover before pdf should fail")
	}
	view, err := a.UploadTextbookPdf(ctx, textbook.ID, []byte("%PDF-1.4 book"), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !view.HasPdf {
		t.Fatalf("pdf upload = %#v", view)
	}
	if _, err := a.UploadTextbookCover(ctx, textbook.ID, nil, "admin"); err == nil {
		t.Fatal("empty cover should fail")
	}
	covered, err := a.UploadTextbookCover(ctx, textbook.ID, []byte("webp-bytes"), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !covered.HasCover {
		t.Fatalf("cover upload = %#v", covered)
	}
	beforeRekey, err := store.GetTextbook(ctx, textbook.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Re-uploading the PDF re-keys the existing cover next to the new hash.
	if _, err := a.UploadTextbookPdf(ctx, textbook.ID, []byte("%PDF-1.4 book v2"), "admin"); err != nil {
		t.Fatal(err)
	}
	rekeyed, err := store.GetTextbook(ctx, textbook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rekeyed.CoverKey == beforeRekey.CoverKey {
		t.Fatal("cover key should follow the new pdf hash")
	}

	a.cosClientOverride = putFailingCOS()
	if _, err := a.UploadTextbookPdf(ctx, textbook.ID, []byte("%PDF-1.4 fail"), "admin"); err == nil || !strings.Contains(err.Error(), "上传 PDF 失败") {
		t.Fatalf("pdf storage failure = %v", err)
	}
	if _, err := a.UploadTextbookCover(ctx, textbook.ID, []byte("webp"), "admin"); err == nil || !strings.Contains(err.Error(), "上传封面失败") {
		t.Fatalf("cover storage failure = %v", err)
	}
}

func TestShareCodeSortingAndCorruption(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	if _, _, err := a.ListShareCodes(ctx, 10, 0, "code", "asc"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateShareCode(ctx, ShareCodeCreateInput{Suffix: "2", Data: map[string]any{"name": "a"}}, "usr-upload"); err != nil {
		t.Fatal(err)
	}
	second, err := a.CreateShareCode(ctx, ShareCodeCreateInput{Suffix: "1", Data: map[string]any{"name": "b"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	views, total, err := a.ListShareCodes(ctx, 10, 0, "code", "desc")
	if err != nil || total != 2 || len(views) != 2 {
		t.Fatalf("sorted share codes = %d %v %v", total, views, err)
	}
	types, _, err := a.ListShareCodes(ctx, 10, 0, "type", "asc")
	if err != nil || len(types) != 2 {
		t.Fatalf("type sort = %v %v", types, err)
	}
	students, _, err := a.ListShareCodes(ctx, 10, 0, "student_id", "asc")
	if err != nil || len(students) != 2 {
		t.Fatalf("student sort = %v %v", students, err)
	}
	page, total, err := a.ListShareCodes(ctx, 1, 1, "unknown", "asc")
	if err != nil || total != 2 || len(page) != 1 {
		t.Fatalf("paged share codes = %v %d %v", page, total, err)
	}
	if _, _, err := a.ListShareCodes(ctx, 10, 99, "code", "asc"); err != nil {
		t.Fatalf("offset beyond total = %v", err)
	}
	if _, _, err := a.GetShareCode(ctx, " "); err == nil {
		t.Fatal("blank share code id should fail")
	}
	if _, _, err := a.GetShareCode(ctx, "missing"); err == nil {
		t.Fatal("missing share code should fail")
	}
	all, _, err := a.ListShareCodes(ctx, 10, 0, "", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all share codes = %v %v", all, err)
	}
	detail, data, err := a.GetShareCode(ctx, all[0].ID)
	if err != nil || data["name"] == nil {
		t.Fatalf("share code detail = %v %v %v", detail, data, err)
	}
	if _, _, err := a.ReadShareCode(ctx, strings.ToLower(second.Code)); err != nil {
		t.Fatalf("read share code is case-insensitive: %v", err)
	}
	if _, _, err := a.GetShareCode(ctx, all[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "UPDATE share_codes SET payload_ciphertext = 'broken' WHERE id = ?", all[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.GetShareCode(ctx, all[0].ID); err == nil {
		t.Fatal("corrupted payload should fail")
	}
	if err := a.DeleteShareCode(ctx, " ", "admin"); err == nil {
		t.Fatal("blank delete should fail")
	}
	if err := a.DeleteShareCode(ctx, "missing", "admin"); err == nil {
		t.Fatal("missing delete should fail")
	}
	if err := a.DeleteShareCode(ctx, all[1].ID, "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSubmissionDownloadFlows(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	pdf := []byte("%PDF-1.4 submission")
	a.cosClientOverride = cosStubMissing()
	ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "试卷甲", Files: []ResourceSubmissionFileInput{pdfFile("试卷甲.pdf", pdf)},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.GetSubmissionFile(ctx, ticket.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = cosStubObjects(map[string][]byte{file.ObjectKey: pdf})
	if _, err := a.CompleteSubmission(ctx, p, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.OpenSubmissionDownload(ctx, ticket.ID, "bogus", ""); err == nil {
		t.Fatal("invalid kind should fail")
	}
	if _, _, err := a.OpenSubmissionDownload(ctx, "missing", "resource", ""); err == nil {
		t.Fatal("missing submission should fail")
	}
	body, filename, err := a.OpenSubmissionDownload(ctx, ticket.ID, "resource", "")
	if err != nil {
		t.Fatal(err)
	}
	body.Body.Close()
	if filename != "试卷甲.pdf" {
		t.Fatalf("download filename = %q", filename)
	}
	if _, _, err := a.OpenSubmissionDownload(ctx, ticket.ID, "resource", "missing-file"); err == nil {
		t.Fatal("unknown file id should fail")
	}

	// Aborting a fresh ticket removes the pending upload.
	a.cosClientOverride = cosStubMissing()
	abortTicket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "取消试卷", Files: []ResourceSubmissionFileInput{pdfFile("取消.pdf", pdf)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AbortSubmission(ctx, p, abortTicket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSubmission(ctx, abortTicket.ID); err == nil {
		t.Fatal("aborted submission should be gone")
	}
	if err := a.AbortSubmission(ctx, p, "missing"); err == nil {
		t.Fatal("aborting a missing submission should fail")
	}

	// A submission without files cannot be downloaded.
	empty, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "空", Files: []ResourceSubmissionFileInput{pdfFile("空.pdf", pdf)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "DELETE FROM submission_files WHERE submission_id = ?", empty.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.OpenSubmissionDownload(ctx, empty.ID, "resource", ""); err == nil {
		t.Fatal("submission without files should fail")
	}
}

func TestCosStubHelperIsConfigured(t *testing.T) {
	a, _, _ := newStorageApp(t)
	if _, err := a.requireStorage(); err != nil {
		t.Fatalf("storage should be configured in tests: %v", err)
	}
	cfg := a.cfg()
	cfg.COSBucket = ""
	cfg.COSSecretID, cfg.COSSecretKey = "", ""
	a.SetConfigForTesting(cfg)
	if _, err := a.requireStorage(); err == nil {
		t.Fatal("storage should be unavailable without credentials")
	}
}

func TestCompleteSubmissionRejectsInvalidUploads(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	other := SessionPrincipal{User: sqlite.User{ID: "someone-else"}}
	pdf := []byte("%PDF-1.4 ticket")

	newTicket := func(name string) ResourceUploadTicketView {
		a.cosClientOverride = cosStubMissing()
		ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
			Kind: "resource", CourseCode: "C1", Name: name, Files: []ResourceSubmissionFileInput{pdfFile(name+".pdf", pdf)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return ticket
	}

	if _, err := a.CompleteSubmission(ctx, p, "missing"); err == nil {
		t.Fatal("missing ticket should fail")
	}
	foreign := newTicket("别人的")
	if _, err := a.CompleteSubmission(ctx, other, foreign.ID); err == nil {
		t.Fatal("另一用户的票据不能完成")
	}
	if err := a.AbortSubmission(ctx, other, foreign.ID); err == nil {
		t.Fatal("另一用户的票据不能取消")
	}
	a.cosClientOverride = cosStubMissing()
	if _, err := a.CompleteSubmission(ctx, p, foreign.ID); err == nil {
		t.Fatal("missing object should fail")
	}
	if _, err := store.GetSubmission(ctx, foreign.ID); err == nil {
		t.Fatal("failed completion should discard the ticket")
	}

	// A head size that does not match the reserved object hash discards it.
	bad := newTicket("坏哈希")
	a.cosClientOverride = newStubCOSClientWithETag(map[string]string{http.MethodGet: string(pdf)}, map[string]int64{http.MethodHead: int64(len(pdf))}, "deadbeef")
	if _, err := a.CompleteSubmission(ctx, p, bad.ID); err == nil {
		t.Fatal("hash mismatch should fail")
	}

	// A zero-sized object is rejected.
	empty := newTicket("空对象")
	a.cosClientOverride = newStubCOSClientWithETag(map[string]string{http.MethodGet: string(pdf)}, map[string]int64{http.MethodHead: 0}, "")
	if _, err := a.CompleteSubmission(ctx, p, empty.ID); err == nil {
		t.Fatal("empty object should fail")
	}

	// A non-PDF prefix is rejected.
	wrongPrefix := newTicket("坏内容")
	wf, err := store.GetSubmissionFile(ctx, wrongPrefix.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = cosStubObjects(map[string][]byte{wf.ObjectKey: []byte("NOPE")})
	if _, err := a.CompleteSubmission(ctx, p, wrongPrefix.ID); err == nil {
		t.Fatal("non-pdf content should fail")
	}

	// A ticket without files is discarded instead of completed.
	noFiles := newTicket("没有文件")
	if _, err := store.DB.ExecContext(ctx, "DELETE FROM submission_files WHERE submission_id = ?", noFiles.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CompleteSubmission(ctx, p, noFiles.ID); err == nil {
		t.Fatal("ticket without files should fail")
	}
	if _, err := store.GetSubmission(ctx, noFiles.ID); err == nil {
		t.Fatal("ticket without files should be discarded")
	}
}

func TestSubmissionReviewAdminFlows(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	cfg := a.cfg()
	cfg.CDNDomain = "https://dl.xuda.live"
	cfg.CDNAuthKey = "cdn-key"
	cfg.CDNURLTTL = 30 * time.Minute
	a.SetConfigForTesting(cfg)
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	pdf := []byte("%PDF-1.4 review")
	a.cosClientOverride = cosStubMissing()
	ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "审核试卷", Files: []ResourceSubmissionFileInput{pdfFile("审核.pdf", pdf), pdfFile("副本.pdf", pdf)},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.GetSubmissionFile(ctx, ticket.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = cosStubObjects(map[string][]byte{first.ObjectKey: pdf})
	if _, err := a.CompleteSubmission(ctx, p, ticket.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := a.SubmissionResourceURL(ctx, "missing", "bogus", 0); err == nil {
		t.Fatal("invalid kind should fail")
	}
	if _, err := a.SubmissionResourceURL(ctx, "missing", "resource", 0); err == nil {
		t.Fatal("missing submission should fail")
	}
	if _, err := a.SubmissionResourceURL(ctx, "missing", "resource", 0); err == nil {
		t.Fatal("url for a missing submission should fail")
	}
	reviewURL, err := a.SubmissionResourceURL(ctx, ticket.ID, "resource", 0)
	if err != nil || !strings.Contains(reviewURL.URL, "sign=") || reviewURL.ExpiresAt == "" {
		t.Fatalf("review url = %+v %v", reviewURL, err)
	}
	if err := a.DeleteSubmissionFileForAdmin(ctx, ticket.ID, "bogus", first.ID, "admin"); err == nil {
		t.Fatal("invalid kind should fail")
	}
	if err := a.DeleteSubmissionFileForAdmin(ctx, ticket.ID, "resource", "missing-file", "admin"); err == nil {
		t.Fatal("missing file should fail")
	}
	if err := a.DeleteSubmissionFileForAdmin(ctx, ticket.ID, "resource", first.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.GetSubmission(ctx, ticket.ID)
	if err != nil || len(remaining.Files) != 1 {
		t.Fatalf("remaining files = %#v %v", remaining, err)
	}
	last := remaining.Files[0]
	if err := a.DeleteSubmissionFileForAdmin(ctx, ticket.ID, "resource", last.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSubmission(ctx, ticket.ID); err == nil {
		t.Fatal("removing the last file should delete the record")
	}

	// Rejecting a whole record removes it.
	rejectTicket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "拒绝试卷", Files: []ResourceSubmissionFileInput{pdfFile("拒绝.pdf", pdf)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rf, err := store.GetSubmissionFile(ctx, rejectTicket.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = cosStubObjects(map[string][]byte{rf.ObjectKey: pdf})
	if _, err := a.CompleteSubmission(ctx, p, rejectTicket.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.RejectSubmissionForAdmin(ctx, rejectTicket.ID, "bogus", "admin"); err == nil {
		t.Fatal("invalid kind should fail")
	}
	if err := a.RejectSubmissionForAdmin(ctx, "missing", "resource", "admin"); err == nil {
		t.Fatal("missing submission should fail")
	}
	if err := a.RejectSubmissionForAdmin(ctx, rejectTicket.ID, "resource", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSubmission(ctx, rejectTicket.ID); err == nil {
		t.Fatal("rejected submission should be gone")
	}
}

func TestAdminDownloadsAndCounters(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	now := time.Now().UTC()
	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = newStubCOSClient(nil, nil)

	if _, _, err := a.OpenTextbookDownload(ctx, "missing"); err == nil {
		t.Fatal("missing textbook download should fail")
	}
	if _, _, err := a.OpenTextbookDownload(ctx, textbook.ID); err == nil {
		t.Fatal("textbook without pdf should fail")
	}
	if err := store.SetTextbookPdfKey(ctx, textbook.ID, "published/textbooks/a.pdf", now); err != nil {
		t.Fatal(err)
	}
	resp, filename, err := a.OpenTextbookDownload(ctx, textbook.ID)
	if err != nil || filename != "教材.pdf" {
		t.Fatalf("textbook download = %q %v", filename, err)
	}
	resp.Body.Close()

	if _, _, err := a.OpenResourceDownload(ctx, "missing"); err == nil {
		t.Fatal("missing resource download should fail")
	}
	if _, _, err := a.OpenResourceDownload(ctx, resource.ID); err == nil {
		t.Fatal("resource without pdf should fail")
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "published/resources/a.pdf", now); err != nil {
		t.Fatal(err)
	}
	resp, filename, err = a.OpenResourceDownload(ctx, resource.ID)
	if err != nil || filename != "试卷.pdf" {
		t.Fatalf("resource download = %q %v", filename, err)
	}
	resp.Body.Close()

	// Storage failures surface as a download error.
	a.cosClientOverride = putFailingCOS()
	if _, _, err := a.OpenResourceDownload(ctx, resource.ID); err == nil {
		t.Fatal("storage failure should fail the download")
	}

	// Download counters only increase once per user per day.
	a.cosClientOverride = newStubCOSClient(nil, nil)
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.GetUploadStats(ctx, p); err != nil {
		t.Fatal(err)
	}
	a.RecordTextbookDownload(ctx, textbook.ID, "usr-upload")
	a.RecordTextbookDownload(ctx, textbook.ID, "usr-upload")
	stats, err := a.GetUploadStats(ctx, p)
	if err != nil || stats.DownloadCount != 0 {
		t.Fatalf("counters = %#v %v", stats, err)
	}
	a.RecordResourceDownload(ctx, resource.ID, "usr-upload")
	a.RecordResourceDownload(ctx, resource.ID, "usr-upload")
	// Unknown ids only log.
	a.RecordTextbookDownload(ctx, "missing", "usr-upload")
	a.RecordResourceDownload(ctx, "missing", "usr-upload")

	// Unused objects are deleted; shared objects are kept.
	otherTextbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材二", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookPdfKey(ctx, otherTextbook.ID, "published/textbooks/shared.pdf", now); err != nil {
		t.Fatal(err)
	}
	a.removeStoredFileIfUnused(ctx, "published/textbooks/shared.pdf")
	a.removeCoverIfUnused(ctx, "published/textbooks/cover.webp")
	cfg := a.cfg()
	cfg.COSSecretID, cfg.COSSecretKey = "", ""
	a.SetConfigForTesting(cfg)
	a.removeStoredFileIfUnused(ctx, "published/textbooks/unused.pdf")
	a.removeCoverIfUnused(ctx, "published/textbooks/unused.webp")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	a.removeStoredFileIfUnused(ctx, "published/textbooks/after-close.pdf")
	a.removeCoverIfUnused(ctx, "published/textbooks/after-close.webp")
	a.RecordTextbookDownload(ctx, textbook.ID, "another-user")
	a.RecordResourceDownload(ctx, resource.ID, "another-user")
}

func TestSubmissionDuplicatesAndStaleCleanup(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	pdf := []byte("%PDF-1.4 duplicate")
	a.cosClientOverride = cosStubMissing()

	first, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "第一份", Files: []ResourceSubmissionFileInput{pdfFile("一.pdf", pdf)},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstFile, err := store.GetSubmissionFile(ctx, first.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = cosStubObjects(map[string][]byte{firstFile.ObjectKey: pdf})
	if _, err := a.CompleteSubmission(ctx, p, first.ID); err != nil {
		t.Fatal(err)
	}

	// The same content in a new ticket is flagged as a duplicate: HEAD answers
	// for both the published key and the submission object.
	a.cosClientOverride = newStubCOSClientWithETag(map[string]string{http.MethodGet: string(pdf)}, map[string]int64{http.MethodHead: int64(len(pdf))}, md5Hex(pdf))
	second, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "第二份", Files: []ResourceSubmissionFileInput{pdfFile("二.pdf", pdf)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A stub that answers HEAD for every key marks the published copy.
	a.cosClientOverride = newStubCOSClientWithETag(map[string]string{http.MethodGet: string(pdf)}, map[string]int64{http.MethodHead: int64(len(pdf))}, md5Hex(pdf))
	view, err := a.CompleteSubmission(ctx, p, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Files) != 1 || !view.Files[0].Duplicate {
		t.Fatalf("duplicate flag = %+v", view.Files)
	}
	// Rejecting the duplicate keeps the shared object used by the first review.
	if err := a.RejectSubmissionForAdmin(ctx, second.ID, "resource", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.OpenSubmissionDownload(ctx, first.ID, "resource", ""); err != nil {
		t.Fatalf("shared object should stay available: %v", err)
	}
	// Completing an already uploaded ticket is a 404.
	if _, err := a.CompleteSubmission(ctx, p, second.ID); err == nil {
		t.Fatal("completed ticket should not complete twice")
	}

	// A stale pending upload is removed by the cleanup pass.
	a.cosClientOverride = cosStubMissing()
	stale, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "过期", Files: []ResourceSubmissionFileInput{pdfFile("过期.pdf", []byte("%PDF-1.4 stale"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "UPDATE resource_submissions SET created_at = ? WHERE id = ?", time.Now().Add(-48*time.Hour).UnixMilli(), stale.ID); err != nil {
		t.Fatal(err)
	}
	a.cleanupStaleUploads(ctx)
	if _, err := store.GetSubmission(ctx, stale.ID); err == nil {
		t.Fatal("stale upload should be cleaned up")
	}

	// Admin listing filters by kind and pages.
	items, total, err := a.ListSubmissionsForAdmin(ctx, "resource", 10, 0)
	if err != nil || total < 1 || len(items) == 0 {
		t.Fatalf("submission list = %d %v %v", total, items, err)
	}
	if _, total, err := a.ListSubmissionsForAdmin(ctx, "textbook", 10, 0); err != nil || total != 0 {
		t.Fatalf("textbook submissions = %d %v", total, err)
	}
	if _, _, err := a.ListSubmissionsForAdmin(ctx, "bogus", 10, 0); err == nil {
		t.Fatal("invalid kind should fail")
	}
}

func TestResourceAIAndURLVariants(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	cfg := a.cfg()
	cfg.CDNDomain = "https://dl.xuda.live"
	cfg.CDNAuthKey = "cdn-key"
	cfg.CDNURLTTL = 30 * time.Minute
	a.SetConfigForTesting(cfg)
	a.cosClientOverride = newStubCOSClient(nil, nil)
	now := time.Now().UTC()
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResourceAIForUser(ctx, resource.ID, "bogus", 0); err == nil {
		t.Fatal("invalid AI mode should fail")
	}
	if view, err := a.ResourceAIForUser(ctx, resource.ID, "", 0); err != nil || view.Available {
		t.Fatalf("no AI output = %+v %v", view, err)
	}
	if view, err := a.ResourceAIForUser(ctx, resource.ID, "manual", 0); err != nil || view.Available {
		t.Fatalf("no manual output = %+v %v", view, err)
	}
	if err := store.SetResourceManualAnalysis(ctx, resource.ID, "ai/papers/"+resource.ID+"/manual.pdf", 42, now); err != nil {
		t.Fatal(err)
	}
	manual, err := a.ResourceAIForUser(ctx, resource.ID, "manual", time.Hour)
	if err != nil || !manual.Available || !strings.Contains(manual.PDFURL, "sign=") {
		t.Fatalf("manual AI = %+v %v", manual, err)
	}

	// A failed task only reports its status.
	if err := store.SetResourcePdfKey(ctx, resource.ID, "published/resources/ai.pdf", now); err != nil {
		t.Fatal(err)
	}
	task, _, err := store.CreateAITask(ctx, resource.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{Status: stringPointer("failed"), Error: stringPointer("boom")}); err != nil {
		t.Fatal(err)
	}
	failed, err := a.ResourceAIForUser(ctx, resource.ID, "basic", 0)
	if err != nil || failed.Available || failed.Status != "failed" || failed.Error != "boom" {
		t.Fatalf("failed task = %+v %v", failed, err)
	}
	// A succeeded task without an output key has nothing to download.
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{Status: stringPointer("succeeded")}); err != nil {
		t.Fatal(err)
	}
	if view, err := a.ResourceAIForUser(ctx, resource.ID, "more", 0); err != nil || view.Available {
		t.Fatalf("succeeded without output = %+v %v", view, err)
	}
	// With outputs both modes return a signed URL.
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		OutputKey: stringPointer("ai/papers/out.pdf"), MoreOutputKey: stringPointer("ai/papers/out-more.pdf"),
		Markdown: stringPointer("basic"), MoreMarkdown: stringPointer("more"),
	}); err != nil {
		t.Fatal(err)
	}
	basic, err := a.ResourceAIForUser(ctx, resource.ID, "basic", 0)
	if err != nil || !basic.Available || basic.Markdown != "basic" || !strings.Contains(basic.PDFURL, "out.pdf") {
		t.Fatalf("basic AI = %+v %v", basic, err)
	}
	more, err := a.ResourceAIForUser(ctx, resource.ID, "more", time.Hour)
	if err != nil || more.Markdown != "more" || !strings.Contains(more.PDFURL, "out-more.pdf") {
		t.Fatalf("more AI = %+v %v", more, err)
	}

	// Archived resources are hidden from the app but visible to the console.
	if _, err := a.SetResourceArchived(ctx, resource.ID, true, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResourceURL(ctx, resource.ID, 0); err == nil {
		t.Fatal("archived resource should not be served to the app")
	}
	if _, err := a.ResourceURLForAdmin(ctx, resource.ID, 0); err != nil {
		t.Fatalf("admin should see archived resources: %v", err)
	}
}

func TestTextbookResourceURLKinds(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	cfg := a.cfg()
	cfg.CDNDomain = "https://dl.xuda.live"
	cfg.CDNAuthKey = "cdn-key"
	cfg.CDNURLTTL = 30 * time.Minute
	a.SetConfigForTesting(cfg)
	a.cosClientOverride = newStubCOSClient(nil, nil)
	now := time.Now().UTC()
	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.TextbookResourceURL(ctx, textbook.ID, "pdf", 0); err == nil {
		t.Fatal("textbook without pdf should fail")
	}
	if err := store.SetTextbookPdfKey(ctx, textbook.ID, "published/textbooks/a.pdf", now); err != nil {
		t.Fatal(err)
	}
	pdf, err := a.TextbookResourceURL(ctx, textbook.ID, "pdf", 0)
	if err != nil || !strings.Contains(pdf.URL, "a.pdf") {
		t.Fatalf("pdf url = %+v %v", pdf, err)
	}
	defaultKind, err := a.TextbookResourceURL(ctx, textbook.ID, "", 0)
	if err != nil || defaultKind.URL == "" {
		t.Fatalf("default kind = %+v %v", defaultKind, err)
	}
	if _, err := a.TextbookResourceURL(ctx, textbook.ID, "cover", 0); err == nil {
		t.Fatal("cover without file should fail")
	}
	if err := store.SetTextbookCoverKey(ctx, textbook.ID, "published/textbooks/a.webp", now); err != nil {
		t.Fatal(err)
	}
	cover, err := a.TextbookResourceURL(ctx, textbook.ID, "cover", 0)
	if err != nil || !strings.Contains(cover.URL, "a.webp") {
		t.Fatalf("cover url = %+v %v", cover, err)
	}
	if _, err := a.TextbookResourceURL(ctx, textbook.ID, "bogus", 0); err == nil {
		t.Fatal("invalid kind should fail")
	}
	if _, err := a.SetTextbookArchived(ctx, textbook.ID, true, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.TextbookResourceURL(ctx, textbook.ID, "pdf", 0); err == nil {
		t.Fatal("archived textbook should not be served to the app")
	}
	if _, err := a.TextbookResourceURLForAdmin(ctx, textbook.ID, "pdf", 0); err != nil {
		t.Fatalf("admin should see archived textbook: %v", err)
	}
}

func TestObserveLoginAttemptCreatesRiskEvent(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	cfg := a.cfg()
	cfg.RiskLoginWindow = time.Minute
	cfg.RiskLoginCount = 2
	cfg.RiskDeviceCount = 1
	a.SetConfigForTesting(cfg)
	device := sqlite.Device{ID: "dev-risk", DeviceSerial: "s", Installation: "i", TokenHash: "h", PublicKey: "k", Platform: "android", AppVersion: "1", CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC()}
	if err := store.CreateDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	a.ObserveLoginAttempt(ctx, device.ID, "source-hash", "malformed_request")
	a.ObserveLoginAttempt(ctx, device.ID, "", "malformed_request")
	events, err := store.ListRiskEvents(ctx, 10, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "login_attempt_burst" || events[0].ObservedCount < 2 {
		t.Fatalf("risk events = %+v", events)
	}
	// A closed store only logs.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	a.ObserveLoginAttempt(ctx, device.ID, "source-hash", "malformed_request")
}

func TestAssetCleanupSharedAndFailingObjects(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	now := time.Now().UTC()
	first, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材甲", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材乙", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	sharedCover := "published/textbooks/shared.webp"
	if err := store.SetTextbookCoverKey(ctx, first.ID, sharedCover, now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookCoverKey(ctx, second.ID, sharedCover, now); err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = newStubCOSClient(nil, nil)
	// The cover is still referenced by the second textbook.
	a.removeCoverIfUnused(ctx, sharedCover)
	// An unreferenced cover with storage failures only logs.
	a.cosClientOverride = putFailingCOS()
	a.removeCoverIfUnused(ctx, "published/textbooks/failing.webp")
	a.removeStoredFileIfUnused(ctx, "published/resources/failing.pdf")

	// RecordResourceDownload increments the contributor counter.
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "贡献试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "published/resources/contrib.pdf", now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourceContributor(ctx, resource.ID, "usr-upload"); err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = newStubCOSClient(nil, nil)
	a.RecordResourceDownload(ctx, resource.ID, "usr-upload")
	stats, err := a.GetUploadStats(ctx, SessionPrincipal{User: sqlite.User{ID: "usr-upload"}})
	if err != nil || stats.DownloadCount != 1 {
		t.Fatalf("contributor downloads = %#v %v", stats, err)
	}

	// Aborting an already aborted ticket is a 404.
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	a.cosClientOverride = cosStubMissing()
	ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "取消两次", Files: []ResourceSubmissionFileInput{pdfFile("取消.pdf", []byte("%PDF-1.4 twice"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AbortSubmission(ctx, p, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.AbortSubmission(ctx, p, ticket.ID); err == nil {
		t.Fatal("second abort should be a 404")
	}
}

func TestSubmissionUploadingLimit(t *testing.T) {
	a, _, ctx := newStorageApp(t)
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	a.cosClientOverride = cosStubMissing()
	for i := 0; i < maxUploadingPerUser; i++ {
		if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
			Kind: "resource", CourseCode: "C1", Name: "排队" + string(rune('A'+i)),
			Files: []ResourceSubmissionFileInput{pdfFile("排队"+string(rune('A'+i))+".pdf", []byte("%PDF-1.4 "+string(rune('A'+i))))},
		}); err != nil {
			t.Fatalf("ticket %d: %v", i, err)
		}
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "超额", Files: []ResourceSubmissionFileInput{pdfFile("超额.pdf", []byte("%PDF-1.4 over"))},
	}); err == nil {
		t.Fatal("too many pending uploads should be refused")
	}
}

func TestDownloadContributorAndStudentIdentity(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	now := time.Now().UTC()
	if err := store.CreateUser(ctx, sqlite.User{ID: "usr-contributor", Status: "active", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := controlcrypto.Encrypt(a.cfg().EncryptionKey, "20260001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO identities(id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at)
		 VALUES ('idn-upload', 'usr-upload', 'xzit-oa', 'hash', '', ?, ?, ?)`, ciphertext, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stats, err := a.GetUploadStats(ctx, SessionPrincipal{User: sqlite.User{ID: "usr-upload"}})
	if err != nil || stats.StudentID != "20260001" {
		t.Fatalf("upload stats = %#v %v", stats, err)
	}
	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookPdfKey(ctx, textbook.ID, "published/textbooks/contrib.pdf", now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookContributor(ctx, textbook.ID, "usr-contributor"); err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = newStubCOSClient(nil, nil)
	a.RecordTextbookDownload(ctx, textbook.ID, "usr-upload")
	if _, downloads, err := store.GetUserStats(ctx, "usr-contributor"); err != nil || downloads != 1 {
		t.Fatalf("contributor downloads = %d %v", downloads, err)
	}
}

func TestRequireDownloadContributionGate(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	// Zero disables the gate.
	if err := a.RequireDownloadContribution(ctx, "usr-upload"); err != nil {
		t.Fatalf("disabled gate = %v", err)
	}
	cfg := a.cfg()
	cfg.DownloadMinContributions = 2
	a.SetConfigForTesting(cfg)

	err := a.RequireDownloadContribution(ctx, "usr-upload")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "contribution_required" || apiErr.Status != http.StatusForbidden {
		t.Fatalf("gate error = %v", err)
	}
	if !strings.Contains(apiErr.Message, "至少贡献 2") || !strings.Contains(apiErr.Message, "当前 0") {
		t.Fatalf("gate message = %q", apiErr.Message)
	}

	now := time.Now().UTC()
	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookContributor(ctx, textbook.ID, "usr-upload"); err != nil {
		t.Fatal(err)
	}
	if err := a.RequireDownloadContribution(ctx, "usr-upload"); err == nil {
		t.Fatal("one contribution should not pass a threshold of two")
	} else if !strings.Contains(err.Error(), "当前 1") {
		t.Fatalf("message after one contribution = %v", err)
	}

	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourceContributor(ctx, resource.ID, "usr-upload"); err != nil {
		t.Fatal(err)
	}
	if err := a.RequireDownloadContribution(ctx, "usr-upload"); err != nil {
		t.Fatalf("two contributions should pass: %v", err)
	}
	// Another user's contributions do not count.
	if err := a.RequireDownloadContribution(ctx, "someone-else"); err == nil {
		t.Fatal("other user should not pass")
	}
	// A closed store surfaces the query error instead of allowing downloads.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.RequireDownloadContribution(ctx, "usr-upload"); err == nil {
		t.Fatal("closed store should fail")
	}
}
