package app

import (
	"context"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func TestResourceListingFilterAndCourseCounts(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C2", Name: "课程二"}, "admin"); err != nil {
		t.Fatal(err)
	}
	for _, in := range []sqlite.ResourceInput{
		{Name: "a.pdf", CourseCode: "C1"},
		{Name: "b.pdf", CourseCode: "C1"},
		{Name: "c.pdf", CourseCode: "C2"},
	} {
		if _, err := store.CreateResource(ctx, in, now); err != nil {
			t.Fatal(err)
		}
	}

	views, total, err := a.ListResourcesForAdmin(ctx, 50, 0, "", "C1", "", "", false)
	if err != nil || total != 2 || len(views) != 2 {
		t.Fatalf("filtered list = %#v, total=%d, err=%v", views, total, err)
	}
	for _, view := range views {
		if view.CourseCode != "C1" || view.HasPdf {
			t.Fatalf("unexpected resource view %#v", view)
		}
	}

	courses, _, err := a.ListCoursesForAdmin(ctx, 50, 0, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, course := range courses {
		counts[course.Code] = course.ResourceCount
	}
	if counts["C1"] != 2 || counts["C2"] != 1 {
		t.Fatalf("course resource counts = %#v", counts)
	}

	if _, err := a.ResourceURL(ctx, views[0].ID, 0); err == nil {
		t.Fatal("a resource without a PDF should not yield a URL")
	}
	if err := a.DeleteResourceForAdmin(ctx, views[0].ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteResourceForAdmin(ctx, views[0].ID, "admin"); err == nil {
		t.Fatal("deleting a missing resource should fail")
	}
}
