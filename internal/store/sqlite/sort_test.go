package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestListSortWhitelists(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// Courses expose a derived resource_count that must be sortable server-side.
	for _, course := range []CourseInput{{Code: "C1", Name: "甲"}, {Code: "C2", Name: "乙"}} {
		if _, err := store.CreateCourse(ctx, course, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateResource(ctx, ResourceInput{Name: "a.pdf", CourseCode: "C1"}, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.CreateResource(ctx, ResourceInput{Name: fmt.Sprintf("b%d.pdf", i), CourseCode: "C2"}, now); err != nil {
			t.Fatal(err)
		}
	}
	courses, err := store.ListCourses(ctx, 10, 0, "", "resource_count", "desc")
	if err != nil || len(courses) != 2 || courses[0].Code != "C2" {
		t.Fatalf("course resource_count sort = %#v, err = %v", courses, err)
	}

	// Error reports sort by an arbitrary whitelisted column, not just occurred_at.
	if err := store.CreateDevice(ctx, Device{ID: "d1", DeviceSerial: "s1", Installation: "i1", TokenHash: "t", PublicKey: "k", Platform: "android", AppVersion: "2", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"bb", "aa", "cc"} {
		if _, err := store.InsertErrorReport(ctx, ErrorReportInput{
			EventID: fmt.Sprintf("e%d", i), DeviceID: "d1", StudentIDHash: "h", Title: title, OccurredAt: now, ReceivedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	reports, _, err := store.ListErrorReports(ctx, 10, 0, "title", "asc")
	if err != nil || len(reports) != 3 || reports[0].Title != "aa" || reports[2].Title != "cc" {
		t.Fatalf("error report title sort = %#v, err = %v", reports, err)
	}
}
