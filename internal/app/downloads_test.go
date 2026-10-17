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

	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "高等数学", CourseCode: "C1"}, now)
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
	stored, err := store.GetTextbook(ctx, textbook.ID)
	if err != nil || stored.DownloadCount != 1 {
		t.Fatalf("textbook download count = %#v, err = %v", stored, err)
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
	if err != nil || storedResource.DownloadCount != 1 {
		t.Fatalf("resource download count = %#v, err = %v", storedResource, err)
	}

	courses, total, err := a.ListCoursesForAdmin(ctx, 50, 0, "", "", "")
	if err != nil || total != 1 || len(courses) != 1 || courses[0].ResourceDownloads != 1 {
		t.Fatalf("courses = %#v, total = %d, err = %v", courses, total, err)
	}
}

func TestPdfDownloadName(t *testing.T) {
	if got := pdfDownloadName("期中/试卷"); got != "期中_试卷.pdf" {
		t.Fatalf("name = %q", got)
	}
	if got := pdfDownloadName("report.PDF"); got != "report.PDF" {
		t.Fatalf("name = %q", got)
	}
	if got := pdfDownloadName(""); got != "资料.pdf" {
		t.Fatalf("name = %q", got)
	}
}
