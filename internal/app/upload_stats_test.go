package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

func TestUploadStatsBindStudentAndContributor(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := controlcrypto.Encrypt(a.Cfg.EncryptionKey, "2023000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO identities(
			id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"idn-test", "usr-upload", "xzit-oa", "hash-test", "alias-test", ciphertext, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材甲", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.4 ok!")
	hash := md5Hex(pdf)
	a.cosClientOverride = newStubCOSClientWithHead(http.StatusNotFound, nil, nil, "")
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}
	ticket, err := a.CreateSubmissionTicket(ctx, p, ResourceSubmissionTicketInput{
		Kind: "textbook", CourseCode: "C1", Name: "教材甲", TargetID: target.ID, MD5: hash, SizeBytes: int64(len(pdf)),
	})
	if err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = newStubCOSClientWithETag(
		map[string]string{http.MethodGet: string(pdf)},
		map[string]int64{http.MethodHead: int64(len(pdf))},
		hash,
	)
	view, err := a.CompleteSubmission(ctx, p, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.StudentID != "2023000001" {
		t.Fatalf("complete student id = %q", view.StudentID)
	}
	items, _, err := a.ListSubmissionsForAdmin(ctx, "textbook", 50, 0)
	if err != nil || len(items) != 1 || items[0].StudentID != "2023000001" {
		t.Fatalf("review items = %#v, err = %v", items, err)
	}
	out, err := a.ApproveSubmissionForAdmin(ctx, ticket.ID, "textbook", "admin")
	if err != nil {
		t.Fatal(err)
	}
	textbook, ok := out["textbook"].(TextbookView)
	if !ok {
		t.Fatalf("approve = %#v", out)
	}
	if textbook.ID != target.ID {
		t.Fatalf("approved textbook = %q, want target %q", textbook.ID, target.ID)
	}
	stored, err := store.GetTextbook(ctx, target.ID)
	if err != nil || stored.ContributorUserID != "usr-upload" || stored.PdfKey == "" {
		t.Fatalf("stored = %#v, err = %v", stored, err)
	}
	contribution, downloads, err := store.GetUserStats(ctx, "usr-upload")
	if err != nil || contribution != 1 || downloads != 0 {
		t.Fatalf("stats = %d/%d, err = %v", contribution, downloads, err)
	}

	// Admin downloads update the content total but not the contributor profile.
	a.RecordTextbookDownload(ctx, textbook.ID, false)
	contribution, downloads, _ = store.GetUserStats(ctx, "usr-upload")
	if contribution != 1 || downloads != 0 {
		t.Fatalf("after admin download stats = %d/%d", contribution, downloads)
	}
	// App downloads count for the contributor.
	a.RecordTextbookDownload(ctx, textbook.ID, true)
	contribution, downloads, _ = store.GetUserStats(ctx, "usr-upload")
	if contribution != 1 || downloads != 1 {
		t.Fatalf("after app download stats = %d/%d", contribution, downloads)
	}
	stats, err := a.GetUploadStats(ctx, p)
	if err != nil || stats.ContributionCount != 1 || stats.DownloadCount != 1 || stats.StudentID != "2023000001" {
		t.Fatalf("upload stats = %#v, err = %v", stats, err)
	}
}

func TestListTextbooksForUploadIncludesCourses(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C2", Name: "课程二"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材甲", CourseCode: "C1", CourseCodes: []string{"C1", "C2"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材乙", CourseCode: "C2"}, now); err != nil {
		t.Fatal(err)
	}
	items, err := a.ListTextbooksForUpload(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %#v, err = %v", items, err)
	}
	if items[0].Name != "教材乙" || items[1].Name != "教材甲" || len(items[1].CourseCodes) != 2 {
		t.Fatalf("items = %#v", items)
	}
}
