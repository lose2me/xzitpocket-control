package app

import (
	"context"
	"testing"
)

func TestTextbookAdminCRUDAndReassign(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()

	created, err := a.CreateTextbookForAdmin(ctx, TextbookWriteInput{Name: "高等数学", Tags: "同济版", CourseCode: "3007B0001"}, "admin")
	if err != nil || created.ID == "" || created.CourseCode != "3007B0001" {
		t.Fatalf("create = %#v, err = %v", created, err)
	}
	if _, err := a.CreateTextbookForAdmin(ctx, TextbookWriteInput{Name: "高等数学", Tags: "同济版", CourseCode: "3007B0001"}, "admin"); err == nil {
		t.Fatal("duplicate textbook create should conflict")
	}
	// Editing fields and reassigning the course keeps one row.
	updated, err := a.UpdateTextbookForAdmin(ctx, created.ID, TextbookWriteInput{Name: "高等数学（上）", Tags: "同济版/第7版", CourseCode: "3007B2106"}, "admin")
	if err != nil || updated.Name != "高等数学（上）" || updated.CourseCode != "3007B2106" {
		t.Fatalf("update = %#v, err = %v", updated, err)
	}
	if _, err := a.UpdateTextbookForAdmin(ctx, "missing", TextbookWriteInput{Name: "x"}, "admin"); err == nil {
		t.Fatal("updating a missing textbook should fail")
	}
	views, total, err := a.ListTextbooksForAdmin(ctx, 50, 0, "高等数学", "", "")
	if err != nil || total != 1 || len(views) != 1 {
		t.Fatalf("list = %#v, total=%d, err=%v", views, total, err)
	}
	if err := a.DeleteTextbookForAdmin(ctx, created.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteTextbookForAdmin(ctx, created.ID, "admin"); err == nil {
		t.Fatal("deleting a missing textbook should fail")
	}
}
