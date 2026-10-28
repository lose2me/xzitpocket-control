package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func TestOpenDownloadsCountsAndNames(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	a.cosClientOverride = newStubCOSClient(
		map[string]string{http.MethodGet: "%PDF-1.4\n"},
		map[string]int64{},
	)

	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "高等数学", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookPdfKey(ctx, textbook.ID, "books/tb/original.pdf", now); err != nil {
		t.Fatal(err)
	}
	resp, filename, err := a.OpenTextbookDownload(ctx, textbook.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.HasPrefix(string(body), "%PDF") || filename != "高等数学.pdf" {
		t.Fatalf("body = %q, filename = %q", body, filename)
	}
	// Admin downloads are not counted as popularity.
	stored, err := store.GetTextbook(ctx, textbook.ID)
	if err != nil || stored.DownloadCount != 0 {
		t.Fatalf("textbook download count = %#v, err = %v", stored, err)
	}
	a.RecordTextbookDownload(ctx, textbook.ID, "usr-upload")
	if stored, err = store.GetTextbook(ctx, textbook.ID); err != nil || stored.DownloadCount != 1 {
		t.Fatalf("app download not counted: %#v, err = %v", stored, err)
	}

	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "期末试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "resources/res/original.pdf", now); err != nil {
		t.Fatal(err)
	}
	resp, filename, err = a.OpenResourceDownload(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if filename != "期末试卷.pdf" {
		t.Fatalf("resource filename = %q", filename)
	}
	storedResource, err := store.GetResource(ctx, resource.ID)
	if err != nil || storedResource.DownloadCount != 0 {
		t.Fatalf("resource download count = %#v, err = %v", storedResource, err)
	}
	a.RecordResourceDownload(ctx, resource.ID, "usr-upload")
	if storedResource, err = store.GetResource(ctx, resource.ID); err != nil || storedResource.DownloadCount != 1 {
		t.Fatalf("resource app download not counted: %#v, err = %v", storedResource, err)
	}

	courses, total, err := a.ListCoursesForAdmin(ctx, 50, 0, "", "", "")
	if err != nil || total != 1 || len(courses) != 1 || courses[0].ResourceCount != 1 {
		t.Fatalf("courses = %#v, total = %d, err = %v", courses, total, err)
	}
}

func TestDownloadFileName(t *testing.T) {
	if got := downloadFileName("期中/试卷", fileFormatPDF); got != "期中_试卷.pdf" {
		t.Fatalf("name = %q", got)
	}
	if got := downloadFileName("report.PDF", fileFormatPDF); got != "report.pdf" {
		t.Fatalf("name = %q", got)
	}
	if got := downloadFileName("", fileFormatPDF); got != "资料.pdf" {
		t.Fatalf("name = %q", got)
	}
	if got := downloadFileName("期末试卷.docx", fileFormatDOCX); got != "期末试卷.docx" {
		t.Fatalf("name = %q", got)
	}
	if got := downloadFileName("期末试卷", fileFormatDOC); got != "期末试卷.doc" {
		t.Fatalf("name = %q", got)
	}
}

func TestStoredFileFormatAndValidation(t *testing.T) {
	cases := []struct {
		key    string
		format string
		prefix []byte
		valid  bool
	}{
		{"submissions/a/original.pdf", fileFormatPDF, []byte("%PDF-1.4"), true},
		{"submissions/a/original.docx", fileFormatDOCX, []byte{'P', 'K', 0x03, 0x04}, true},
		{"submissions/a/original.doc", fileFormatDOC, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, true},
		{"submissions/a/original.docx", fileFormatDOCX, []byte("%PDF-1.4"), false},
		{"submissions/a/original.doc", fileFormatDOC, []byte("PK\x03\x04"), false},
		{"resources/legacy/original", fileFormatPDF, []byte("%PDF-1.4"), true},
	}
	for _, tc := range cases {
		if got := storedFileFormat(tc.key); got != tc.format {
			t.Fatalf("storedFileFormat(%q) = %q, want %q", tc.key, got, tc.format)
		}
		if got := storedFileIsValid(tc.key, tc.prefix); got != tc.valid {
			t.Fatalf("storedFileIsValid(%q, %q) = %v, want %v", tc.key, tc.prefix, got, tc.valid)
		}
	}
}
