package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadCoursesInsertThenFillEmpty(t *testing.T) {
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

	accepted, err := store.UploadCourses(ctx, []CourseInput{
		{Code: "3007B2106", Name: "数学分析（Ⅲ）"},
		{Code: "3015G0003", Name: "中国近现代史纲要"},
	}, now)
	if err != nil || accepted != 2 {
		t.Fatalf("first upload accepted = %d, err = %v", accepted, err)
	}

	// A duplicate course number does not create a row and never overwrites a
	// stored name.
	accepted, err = store.UploadCourses(ctx, []CourseInput{
		{Code: "3007B2106", Name: "改后的名字"},
	}, now.Add(time.Minute))
	if err != nil || accepted != 0 {
		t.Fatalf("duplicate upload accepted = %d, err = %v", accepted, err)
	}
	items, err := store.ListCourses(ctx, 50, 0, "3007B2106", "", "")
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %#v, err = %v", items, err)
	}
	if items[0].Name != "数学分析（Ⅲ）" {
		t.Fatalf("existing name was overwritten: %#v", items[0])
	}
	total, err := store.CountCourses(ctx, "")
	if err != nil || total != 2 {
		t.Fatalf("count = %d, err = %v", total, err)
	}
}

func TestCourseAdminCRUD(t *testing.T) {
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

	created, err := store.CreateCourse(ctx, CourseInput{Code: "3007P0102", Name: "离散数学"}, now)
	if err != nil || created.ID == "" {
		t.Fatalf("create = %#v, err = %v", created, err)
	}
	if _, err := store.CreateCourse(ctx, CourseInput{Code: "3007P0102", Name: "另一门"}, now); !errors.Is(err, ErrCourseExists) {
		t.Fatalf("duplicate create error = %v, want ErrCourseExists", err)
	}
	if err := store.UpdateCourse(ctx, created.ID, CourseInput{Code: "3007P0102", Name: "离散数学（新）"}, now); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCourse(ctx, created.ID)
	if err != nil || got.Name != "离散数学（新）" {
		t.Fatalf("updated = %#v, err = %v", got, err)
	}
	if err := store.UpdateCourse(ctx, "missing", CourseInput{Code: "X", Name: "课程"}, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("updating a missing course error = %v, want sql.ErrNoRows", err)
	}
	if err := store.DeleteCourse(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCourse(ctx, created.ID); err == nil {
		t.Fatal("deleting a missing course should fail")
	}
}
