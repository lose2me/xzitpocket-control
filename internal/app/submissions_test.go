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
	"xzitpocket-control/internal/store/sqlite"
)

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
	client := cloud.NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: cosRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodHead:
			return cosStubResponse(http.StatusOK, "", sizes[req.Method]), nil
		case http.MethodGet:
			return cosStubResponse(http.StatusOK, methodBodies[req.Method], int64(len(methodBodies[req.Method]))), nil
		default:
			return cosStubResponse(http.StatusMethodNotAllowed, "", 0), nil
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

	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "other", CourseCode: "C1", Name: "x", SizeBytes: 10}); err == nil {
		t.Fatal("unknown kind should be rejected")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "missing", Name: "x", SizeBytes: 10}); err == nil {
		t.Fatal("unknown course should be rejected")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", Name: "x", SizeBytes: 10}); err == nil {
		t.Fatal("textbook without a target should be rejected")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", Name: "x", TargetID: "missing", SizeBytes: 10}); err == nil {
		t.Fatal("missing target textbook should be rejected")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", Name: "x", TargetID: other.ID, SizeBytes: 10}); err == nil {
		t.Fatal("textbook from another course should be rejected")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "textbook", CourseCode: "C1", TargetID: target.ID, SizeBytes: 10}); err == nil {
		t.Fatal("missing object storage should be reported")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", SizeBytes: 10}); err == nil {
		t.Fatal("empty resource name should be rejected")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", Name: "x", SizeBytes: 0}); err == nil {
		t.Fatal("empty file should be rejected")
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{Kind: "resource", CourseCode: "C1", Name: "x", SizeBytes: maxSubmissionBytes + 1}); err == nil {
		t.Fatal("oversized file should be rejected")
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
			Kind: "resource", CourseCode: "C1", Name: "试卷", PdfKey: "submissions/x/original.pdf",
			Status: "pending_review", SubmittedByUserID: p.User.ID, SubmittedByName: "张三",
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "新试卷", SizeBytes: 10,
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
			Kind: "resource", CourseCode: "C1", Name: "上传中", PdfKey: "submissions/y/original.pdf",
			Status: "pending_upload", SubmittedByUserID: other.User.ID, SubmittedByName: "张三",
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CreateSubmissionTicket(ctx, other, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "另一份", SizeBytes: 10,
	}); err == nil {
		t.Fatal("too many concurrent uploads should be rejected")
	}
}

func TestCompleteSubmissionVerifiesUpload(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = newStubCOSClient(
		map[string]string{http.MethodGet: "%PDF-"},
		map[string]int64{http.MethodHead: 12},
	)

	ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "试卷甲", SizeBytes: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.CompleteSubmission(ctx, p, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "pending_review" || view.SizeBytes != 12 || !view.HasPdf {
		t.Fatalf("completed view = %#v", view)
	}
	stored, err := store.GetSubmission(ctx, ticket.ID)
	if err != nil || stored.Status != "pending_review" {
		t.Fatalf("stored = %#v, err = %v", stored, err)
	}

	// A non-PDF object is removed and never enters the queue.
	a.cosClientOverride = newStubCOSClient(
		map[string]string{http.MethodGet: "NOPE!"},
		map[string]int64{http.MethodHead: 5},
	)
	badTicket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "resource", CourseCode: "C1", Name: "假试卷", SizeBytes: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CompleteSubmission(ctx, p, badTicket.ID); err == nil {
		t.Fatal("a non-PDF upload should be rejected")
	}
	if _, err := store.GetSubmission(ctx, badTicket.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rejected upload should be removed, err = %v", err)
	}
}

func TestApproveSubmissionCreatesContent(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	textbookKey := "submissions/sub-tb/original.pdf"
	textbook, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "textbook", CourseCode: "C1", Name: "教材甲", Tags: "高教社", PdfKey: textbookKey,
		SizeBytes: 12, Status: "pending_review", SubmittedByUserID: "usr-upload", SubmittedByName: "张三",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := a.ListSubmissionsForAdmin(ctx, "textbook", 50, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].SubmittedByName != "张三" {
		t.Fatalf("list = %#v, total = %d, err = %v", items, total, err)
	}
	out, err := a.ApproveSubmissionForAdmin(ctx, textbook.ID, "textbook", "admin")
	if err != nil {
		t.Fatal(err)
	}
	view, ok := out["textbook"].(TextbookView)
	if !ok || !view.HasPdf || view.CourseCode != "C1" {
		t.Fatalf("approve = %#v", out)
	}
	stored, err := store.GetTextbook(ctx, view.ID)
	if err != nil || stored.PdfKey != textbookKey {
		t.Fatalf("stored = %#v, err = %v", stored, err)
	}
	if _, err := store.GetSubmission(ctx, textbook.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reviewed submission should be removed, err = %v", err)
	}

	resourceKey := "submissions/sub-res/original.pdf"
	resource, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "resource", CourseCode: "C1", Name: "试卷甲", PdfKey: resourceKey,
		SizeBytes: 12, Status: "pending_review", SubmittedByUserID: "usr-upload", SubmittedByName: "张三",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	out, err = a.ApproveSubmissionForAdmin(ctx, resource.ID, "resource", "admin")
	if err != nil {
		t.Fatal(err)
	}
	resourceView, ok := out["resource"].(ResourceView)
	if !ok || !resourceView.HasPdf || resourceView.CourseCode != "C1" {
		t.Fatalf("approve resource = %#v", out)
	}
	storedResource, err := store.GetResource(ctx, resourceView.ID)
	if err != nil || storedResource.PdfKey != resourceKey {
		t.Fatalf("stored resource = %#v, err = %v", storedResource, err)
	}
}

func TestApproveDuplicateSubmissionKeepsQueueRow(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateTextbookForAdmin(ctx, TextbookWriteInput{Name: "教材甲", Tags: "高教社", CourseCode: "C1"}, "admin"); err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "textbook", CourseCode: "C1", Name: "教材甲", Tags: "高教社", PdfKey: "submissions/x/original.pdf",
		Status: "pending_review", SubmittedByUserID: "usr-upload", SubmittedByName: "张三",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApproveSubmissionForAdmin(ctx, item.ID, "textbook", "admin"); err == nil {
		t.Fatal("duplicate content should be reported")
	}
	if _, err := store.GetSubmission(ctx, item.ID); err != nil {
		t.Fatalf("conflicting submission should stay queued, err = %v", err)
	}
}

func TestRejectSubmissionRemovesQueueRow(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	item, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "resource", CourseCode: "C1", Name: "试卷甲", PdfKey: "submissions/y/original.pdf",
		Status: "pending_review", SubmittedByUserID: "usr-upload", SubmittedByName: "张三",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
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

func TestPendingUploadIsHiddenFromReviewQueue(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	if _, err := store.CreateSubmission(ctx, sqlite.SubmissionInput{
		Kind: "textbook", CourseCode: "C1", Name: "上传中", PdfKey: "submissions/z/original.pdf",
		Status: "pending_upload", SubmittedByUserID: "usr-upload", SubmittedByName: "张三",
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	items, total, err := a.ListSubmissionsForAdmin(ctx, "textbook", 50, 0)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("pending upload should be hidden: %#v, %d, %v", items, total, err)
	}
}
