package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/store/sqlite"
)

func newCourseApp(t *testing.T) (*App, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{TokenPepper: []byte("token"), IDPepper: []byte("id"), EncryptionKey: []byte("01234567890123456789012345678901"), AdminKey: "test"}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateUser(context.Background(), sqlite.User{ID: "usr-upload", Status: "active", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	return a, store
}

func TestUploadCoursesValidation(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}

	if _, err := a.UploadCourses(ctx, p, CourseUploadInput{}); err == nil {
		t.Fatal("empty course batch should be rejected")
	}
	if _, err := a.UploadCourses(ctx, p, CourseUploadInput{Items: []CourseUploadItem{{Name: "只有名字"}}}); err == nil {
		t.Fatal("missing course code should be rejected")
	}
	if _, err := a.UploadCourses(ctx, p, CourseUploadInput{Items: []CourseUploadItem{{Code: "X\n1", Name: "课程"}}}); err == nil {
		t.Fatal("control characters in course code should be rejected")
	}
	tooMany := make([]CourseBookInput, maxCourseBooks+1)
	for i := range tooMany {
		tooMany[i] = CourseBookInput{Name: "书"}
	}
	if _, err := a.UploadCourses(ctx, p, CourseUploadInput{Items: []CourseUploadItem{{Code: "C1", Name: "课程", Books: tooMany}}}); err == nil {
		t.Fatal("too many books should be rejected")
	}
}

func TestUploadCoursesStoresTextbooks(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	p := SessionPrincipal{User: sqlite.User{ID: "usr-upload"}}

	out, err := a.UploadCourses(ctx, p, CourseUploadInput{Items: []CourseUploadItem{
		{Code: "3007B2106", Name: "数学分析（Ⅲ）", Books: []CourseBookInput{{Name: "数学分析", Tags: "高教社"}}},
	}})
	if err != nil || out["accepted"] != 1 || out["accepted_books"] != 1 {
		t.Fatalf("upload = %#v, err = %v", out, err)
	}
	views, total, err := a.ListCoursesForAdmin(ctx, 50, 0, "", "", "")
	if err != nil || total != 1 || len(views) != 1 {
		t.Fatalf("list = %#v, total=%d, err=%v", views, total, err)
	}
	if len(views[0].Books) != 1 || views[0].Books[0].Name != "数学分析" {
		t.Fatalf("course view books = %#v", views[0].Books)
	}
	grouped, err := store.ListTextbooksForCourses(ctx, []string{"3007B2106"})
	if err != nil || len(grouped["3007B2106"]) != 1 {
		t.Fatalf("textbooks = %#v, err = %v", grouped, err)
	}
}

func TestCourseAdminCRUDConflictAndNotFound(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()

	created, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "3007P0102", Name: "软件工程"}, "admin")
	if err != nil || created.ID == "" {
		t.Fatalf("create = %#v, err = %v", created, err)
	}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "3007P0102", Name: "别的"}, "admin"); err == nil {
		t.Fatal("duplicate course code create should conflict")
	}
	if _, err := a.UpdateCourseForAdmin(ctx, created.ID, CourseWriteInput{Code: "3007P0102", Name: "软件工程（新）"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateCourseForAdmin(ctx, "missing", CourseWriteInput{Code: "X", Name: "课程"}, "admin"); err == nil {
		t.Fatal("updating a missing course should fail")
	}
	if err := a.DeleteCourseForAdmin(ctx, created.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteCourseForAdmin(ctx, created.ID, "admin"); err == nil {
		t.Fatal("deleting a missing course should fail")
	}
}
