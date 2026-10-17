package app

import (
	"context"
	"testing"
)

func TestResourceAdminCRUDAndReassign(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()

	created, err := a.CreateResourceForAdmin(ctx, ResourceWriteInput{Name: "期末真题", Tags: "2024", CourseCode: "3007B0001"}, "admin")
	if err != nil || created.ID == "" || created.CourseCode != "3007B0001" || created.HasPdf {
		t.Fatalf("create = %#v, err = %v", created, err)
	}
	if _, err := a.CreateResourceForAdmin(ctx, ResourceWriteInput{Name: "期末真题", Tags: "2024", CourseCode: "3007B0001"}, "admin"); err == nil {
		t.Fatal("duplicate resource create should conflict")
	}
	// Editing fields and reassigning the course keeps one row.
	updated, err := a.UpdateResourceForAdmin(ctx, created.ID, ResourceWriteInput{Name: "期末真题（含答案）", Tags: "2024/答案", CourseCode: "3007B2106"}, "admin")
	if err != nil || updated.Name != "期末真题（含答案）" || updated.CourseCode != "3007B2106" {
		t.Fatalf("update = %#v, err = %v", updated, err)
	}
	if _, err := a.UpdateResourceForAdmin(ctx, "missing", ResourceWriteInput{Name: "x"}, "admin"); err == nil {
		t.Fatal("updating a missing resource should fail")
	}
	views, total, err := a.ListResourcesForAdmin(ctx, 50, 0, "期末真题", "", "")
	if err != nil || total != 1 || len(views) != 1 {
		t.Fatalf("list = %#v, total=%d, err=%v", views, total, err)
	}
	if _, err := a.ResourceURL(ctx, created.ID, 0); err == nil {
		t.Fatal("a resource without a PDF should not yield a URL")
	}
	if err := a.DeleteResourceForAdmin(ctx, created.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteResourceForAdmin(ctx, created.ID, "admin"); err == nil {
		t.Fatal("deleting a missing resource should fail")
	}
}
