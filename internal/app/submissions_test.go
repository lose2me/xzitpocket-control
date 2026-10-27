package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/cloud"
	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/store/sqlite"
)

const testMD5 = "0123456789abcdef0123456789abcdef"

type cosRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cosRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func cosStubResponse(status int, body string, contentLength int64) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(strings.NewReader(body)),
		Header:        make(http.Header),
		ContentLength: contentLength,
	}
}

func newStubCOSClient(methodBodies map[string]string, sizes map[string]int64) *cloud.COSClient {
	return newStubCOSClientWithETag(methodBodies, sizes, "")
}

func newStubCOSClientWithETag(methodBodies map[string]string, sizes map[string]int64, etag string) *cloud.COSClient {
	return newStubCOSClientWithHead(http.StatusOK, methodBodies, sizes, etag)
}

func newStubCOSClientWithHead(headStatus int, methodBodies map[string]string, sizes map[string]int64, etag string) *cloud.COSClient {
	client := cloud.NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: cosRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodHead:
			resp := cosStubResponse(headStatus, "", sizes[req.Method])
			if etag != "" {
				resp.Header.Set("ETag", `"`+etag+`"`)
			}
			return resp, nil
		case http.MethodGet:
			return cosStubResponse(http.StatusOK, methodBodies[req.Method], int64(len(methodBodies[req.Method]))), nil
		default:
			// PUT covers direct uploads and server-side copies; DELETE cleans up
			// superseded objects.
			return cosStubResponse(http.StatusOK, "", 0), nil
		}
	})}
	return client
}

func pdfFile(name string, data []byte) ResourceSubmissionFileInput {
	return ResourceSubmissionFileInput{Name: name, Format: "pdf", MD5: md5Hex(data), SizeBytes: int64(len(data))}
}

func cosStubMissing() *cloud.COSClient {
	return newStubCOSClientWithHead(http.StatusNotFound, nil, nil, "")
}

// cosStubObjects serves HEAD/GET per object key so a submit with several
// different files can be verified.
func cosStubObjects(objects map[string][]byte) *cloud.COSClient {
	client := cloud.NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: cosRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		data, ok := objects[strings.TrimPrefix(req.URL.Path, "/")]
		if !ok {
			return cosStubResponse(http.StatusNotFound, "", 0), nil
		}
		switch req.Method {
		case http.MethodHead:
			resp := cosStubResponse(http.StatusOK, "", int64(len(data)))
			resp.Header.Set("ETag", `"`+md5Hex(data)+`"`)
			return resp, nil
		case http.MethodGet:
			return cosStubResponse(http.StatusOK, string(data), int64(len(data))), nil
		default:
			return cosStubResponse(http.StatusOK, "", 0), nil
		}
	})}
	return client
}

func TestCreateSubmissionTicketValidation(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C2", Name: "课程二"}, "admin"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材甲", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材乙", CourseCode: "C2"}, now)
	if err != nil {
		t.Fatal(err)
	}
	file := pdfFile("试卷.pdf", []byte("%PDF-1.4 x"))
	docx := ResourceSubmissionFileInput{Name: "试卷.docx", Format: "docx", MD5: testMD5, SizeBytes: 10}
	exe := ResourceSubmissionFileInput{Name: "试卷.exe", Format: "exe", MD5: testMD5, SizeBytes: 10}
	empty := ResourceSubmissionFileInput{Name: "空.pdf", Format: "pdf", MD5: testMD5, SizeBytes: 0}
	tooBig := ResourceSubmissionFileInput{Name: "大.pdf", Format: "pdf", MD5: testMD5, SizeBytes: maxSubmissionBytes + 1}
	badHash := ResourceSubmissionFileInput{Name: "坏.pdf", Format: "pdf", MD5: "not-a-hash", SizeBytes: 10}

	cases := []struct {
		name string
		in   ResourceSubmissionTicketInput
	}{
		{"unknown kind", ResourceSubmissionTicketInput{Kind: "other", CourseCode: "C1", Name: "x", Files: []ResourceSubmissionFileInput{file}}},
		{"missing md5", ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", Name: "x", Files: []ResourceSubmissionFileInput{badHash}}},
		{"unknown course", ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "missing", Name: "x", Files: []ResourceSubmissionFileInput{file}}},
		{"textbook without target", ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", Name: "x", Files: []ResourceSubmissionFileInput{file}}},
		{"missing target", ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", Name: "x", TargetID: "missing", Files: []ResourceSubmissionFileInput{file}}},
		{"target from another course", ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", Name: "x", TargetID: other.ID, Files: []ResourceSubmissionFileInput{file}}},
		{"no files", ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", Name: "x"}},
		{"empty file", ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", Name: "x", Files: []ResourceSubmissionFileInput{empty}}},
		{"oversized file", ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", Name: "x", Files: []ResourceSubmissionFileInput{tooBig}}},
		{"unsupported format", ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", Name: "x", Files: []ResourceSubmissionFileInput{exe}}},
		{"textbook docx", ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", TargetID: target.ID, Files: []ResourceSubmissionFileInput{docx}}},
	}
	for _, tc := range cases {
		if _, err := a.CreateSubmissionTicket(ctx, p, tc.in); err == nil {
			t.Fatalf("%s should be rejected", tc.name)
		}
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "textbook", CourseCode: "C1", TargetID: target.ID, Files: []ResourceSubmissionFileInput{file},
	}); err == nil {
		t.Fatal("missing object storage should be reported")
	}
}

// One submit is one review record, even when it carries several files.
func TestSubmitWithSeveralFilesIsOneRecord(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	paper := []byte("%PDF-1.4 paper")
	answer := []byte("%PDF-1.4 answer")
	text := []byte("%PDF-1.4 notes")
	a.cosClientOverride = cosStubMissing()
	ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "第一次提交", Note: "期中三件套",
		Files: []ResourceSubmissionFileInput{
			pdfFile("试卷甲.pdf", paper),
			pdfFile("答案甲.pdf", answer),
			{Name: "说明.pdf", Format: "pdf", MD5: md5Hex(text), SizeBytes: int64(len(text))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ticket.Files) != 3 {
		t.Fatalf("ticket files = %#v", ticket.Files)
	}
	for _, upload := range ticket.Files {
		if upload.UploadURL == "" {
			t.Fatalf("file without upload url: %#v", upload)
		}
	}

	objects := map[string][]byte{}
	for _, upload := range ticket.Files {
		file, err := store.GetSubmissionFile(ctx, upload.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch upload.Name {
		case "试卷甲.pdf":
			objects[file.ObjectKey] = paper
		case "答案甲.pdf":
			objects[file.ObjectKey] = answer
		default:
			objects[file.ObjectKey] = text
		}
	}
	a.cosClientOverride = cosStubObjects(objects)
	view, err := a.CompleteSubmission(ctx, p, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Files) != 3 || view.SizeBytes != int64(len(paper)+len(answer)+len(text)) {
		t.Fatalf("completed view = %#v", view)
	}
	items, total, err := a.ListSubmissionsForAdmin(ctx, "resource", 50, 0)
	if err != nil || total != 1 || len(items) != 1 || len(items[0].Files) != 3 {
		t.Fatalf("queue = %#v total=%d err=%v", items, total, err)
	}
}

// Content that is already published is accepted but marked as a duplicate.
func TestDuplicateFileIsMarkedButAccepted(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	paper := []byte("%PDF-1.4 paper")
	a.cosClientOverride = cosStubObjects(map[string][]byte{
		papersPublishedPrefix + md5Hex(paper) + ".pdf": paper,
	})
	ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "重复试卷", Files: []ResourceSubmissionFileInput{pdfFile("试卷甲.pdf", paper)},
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := store.ListSubmissionFiles(ctx, []string{ticket.ID})
	if err != nil || len(files[ticket.ID]) != 1 || !files[ticket.ID][0].Duplicate {
		t.Fatalf("duplicate file = %#v err=%v", files[ticket.ID], err)
	}
	// The upload itself still targets the submissions prefix.
	a.cosClientOverride = cosStubObjects(map[string][]byte{
		files[ticket.ID][0].ObjectKey: paper,
	})
	if _, err := a.CompleteSubmission(ctx, p, ticket.ID); err != nil {
		t.Fatal(err)
	}
	items, _, err := a.ListSubmissionsForAdmin(ctx, "resource", 50, 0)
	if err != nil || len(items) != 1 || !items[0].Files[0].Duplicate {
		t.Fatalf("queue = %#v err=%v", items, err)
	}
}

func TestDeleteSubmissionFileHidesEmptyRecord(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	item, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "resource", CourseCode: "C1", Name: "试卷甲", Status: "pending_review",
		SubmittedByUserID: "usr-upload", SubmittedByName: "同学",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.AddSubmissionFile(ctx, sqlite.SubmissionFileInput{
		SubmissionID: item.ID, Name: "试卷甲.pdf", ObjectKey: papersSubmissionsPrefix + "one.pdf", Format: "pdf", SizeBytes: 10,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddSubmissionFile(ctx, sqlite.SubmissionFileInput{
		SubmissionID: item.ID, Name: "答案甲.pdf", ObjectKey: papersSubmissionsPrefix + "two.pdf", Format: "pdf", SizeBytes: 5,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if queued, err := store.ListSubmissions(ctx, "resource", 10, 0); err != nil || len(queued) != 1 {
		t.Fatalf("queued = %#v err=%v", queued, err)
	}

	a.cosClientOverride = cosStubMissing()
	if err := a.DeleteSubmissionFileForAdmin(ctx, item.ID, "resource", first.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.GetSubmission(ctx, item.ID)
	if err != nil || len(remaining.Files) != 1 || remaining.Files[0].ID != second.ID {
		t.Fatalf("after first delete = %#v err=%v", remaining, err)
	}

	if err := a.DeleteSubmissionFileForAdmin(ctx, item.ID, "resource", second.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSubmission(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("submission should be gone, err=%v", err)
	}
	if err := a.DeleteSubmissionFileForAdmin(ctx, item.ID, "resource", second.ID, "admin"); err == nil {
		t.Fatal("deleting a file of a missing record should fail")
	}
}

func TestRejectSubmissionRemovesRecord(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	item, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "resource", CourseCode: "C1", Name: "试卷甲", Status: "pending_review",
		SubmittedByUserID: "usr-upload", SubmittedByName: "同学",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddSubmissionFile(ctx, sqlite.SubmissionFileInput{
		SubmissionID: item.ID, Name: "试卷甲.pdf", ObjectKey: papersSubmissionsPrefix + "one.pdf", Format: "pdf", SizeBytes: 10,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = cosStubMissing()
	if err := a.RejectSubmissionForAdmin(ctx, item.ID, "resource", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSubmission(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rejected submission should be removed, err = %v", err)
	}
	if err := a.RejectSubmissionForAdmin(ctx, item.ID, "resource", "admin"); err == nil {
		t.Fatal("rejecting a missing submission should fail")
	}
}

func TestSubmissionTicketHonoursAcceptSwitch(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg()
	cfg.IngestSubmissions = config.IngestSwitch{Disabled: true}
	a.SetConfigForTesting(cfg)
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "试卷甲",
		Files: []ResourceSubmissionFileInput{pdfFile("试卷甲.pdf", []byte("%PDF-1.4 x"))},
	}); err == nil {
		t.Fatal("uploads must be rejected while the switch is off")
	}
}

func TestTelemetryIsDroppedWhenDisabled(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()
	cfg := a.cfg()
	cfg.IngestEvents = config.IngestSwitch{Disabled: true}
	a.SetConfigForTesting(cfg)
	out, err := a.InsertTelemetry(ctx, DevicePrincipal{}, nil, []TelemetryEventInput{
		{EventID: "evt-1", Type: "app_start", OccurredAt: time.Now().UTC().Format(time.RFC3339)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["accepted"] != 0 {
		t.Fatalf("telemetry accepted while disabled: %#v", out)
	}
}

func TestCompleteSubmissionVerifiesUpload(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.4 ok!")
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
	view, err := a.CompleteSubmission(ctx, p, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "pending_review" || view.SizeBytes != int64(len(pdf)) || len(view.Files) != 1 {
		t.Fatalf("completed view = %#v", view)
	}

	// A non-PDF object is removed and never enters the queue.
	bad := []byte("NOPE!")
	a.cosClientOverride = cosStubMissing()
	badTicket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "假试卷", Files: []ResourceSubmissionFileInput{pdfFile("假试卷.pdf", bad)},
	})
	if err != nil {
		t.Fatal(err)
	}
	badFile, err := store.GetSubmissionFile(ctx, badTicket.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = cosStubObjects(map[string][]byte{badFile.ObjectKey: bad})
	if _, err := a.CompleteSubmission(ctx, p, badTicket.ID); err == nil {
		t.Fatal("a non-PDF upload should be rejected")
	}
	if _, err := store.GetSubmission(ctx, badTicket.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rejected upload should be removed, err = %v", err)
	}
}

func TestCreateSubmissionTicketLimits(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < maxPendingSubmissionsPerUser; i++ {
		if _, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
			Kind: "resource", CourseCode: "C1", Name: "试卷",
			Status: "pending_review", SubmittedByUserID: p.User.ID, SubmittedByName: "张三",
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	a.cosClientOverride = cosStubMissing()
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "新试卷", Files: []ResourceSubmissionFileInput{pdfFile("新试卷.pdf", []byte("%PDF-1.4 x"))},
	}); err == nil {
		t.Fatal("a full review queue should reject new tickets")
	}

	otherNow := time.Now().UTC()
	if err := store.CreateUser(ctx, sqlite.User{ID: "usr-upload-2", Status: "active", CreatedAt: otherNow, LastLoginAt: otherNow}); err != nil {
		t.Fatal(err)
	}
	other := SessionPrincipal{User: sqlite.User{ID: "usr-upload-2"}}
	for i := 0; i < maxUploadingPerUser; i++ {
		if _, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
			Kind: "resource", CourseCode: "C1", Name: "上传中",
			Status: "pending_upload", SubmittedByUserID: other.User.ID, SubmittedByName: "张三",
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CreateSubmissionTicket(ctx, other, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "另一份", Files: []ResourceSubmissionFileInput{pdfFile("另一份.pdf", []byte("%PDF-1.4 y"))},
	}); err == nil {
		t.Fatal("too many concurrent uploads should be rejected")
	}
}

func TestPendingUploadIsHiddenFromReviewQueue(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	item, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "textbook", CourseCode: "C1", Name: "上传中",
		Status: "pending_upload", SubmittedByUserID: "usr-upload", SubmittedByName: "张三",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddSubmissionFile(ctx, sqlite.SubmissionFileInput{
		SubmissionID: item.ID, Name: "上传中.pdf", ObjectKey: booksSubmissionsPrefix + "x.pdf", Format: "pdf", SizeBytes: 12,
	}, now); err != nil {
		t.Fatal(err)
	}
	items, total, err := a.ListSubmissionsForAdmin(ctx, "textbook", 50, 0)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("pending upload should be hidden: %#v, %d, %v", items, total, err)
	}
}
