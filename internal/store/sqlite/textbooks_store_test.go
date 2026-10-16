package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadTextbooksInsertAndKeepAssociation(t *testing.T) {
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

	accepted, err := store.UploadTextbooks(ctx, []TextbookInput{
		{Name: "数学分析", Tags: "高教社", CourseCode: "3007B2106"},
		{Name: "数学分析", Tags: "高教社", CourseCode: "3007B2106"},
		{Name: "离散数学", Tags: "清华社", CourseCode: "3007P0102"},
	}, now)
	if err != nil || accepted != 2 {
		t.Fatalf("accepted = %d, err = %v", accepted, err)
	}

	// Re-uploading the same key is a no-op and keeps the stored association.
	accepted, err = store.UploadTextbooks(ctx, []TextbookInput{
		{Name: "数学分析", Tags: "高教社", CourseCode: "3007B2106"},
	}, now.Add(time.Minute))
	if err != nil || accepted != 0 {
		t.Fatalf("duplicate accepted = %d, err = %v", accepted, err)
	}
	grouped, err := store.ListTextbooksForCourses(ctx, []string{"3007B2106", "3007P0102"})
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped["3007B2106"]) != 1 || grouped["3007B2106"][0].Name != "数学分析" {
		t.Fatalf("grouped textbooks = %#v", grouped)
	}
	if len(grouped["3007P0102"]) != 1 || grouped["3007P0102"][0].Tags != "清华社" {
		t.Fatalf("grouped textbooks = %#v", grouped)
	}
	total, err := store.CountTextbooks(ctx, "")
	if err != nil || total != 2 {
		t.Fatalf("count = %d, err = %v", total, err)
	}
}

func TestTextbookAdminCRUDAndReassign(t *testing.T) {
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

	created, err := store.CreateTextbook(ctx, TextbookInput{Name: "高等数学", Tags: "同济版", CourseCode: "C1"}, now)
	if err != nil || created.ID == "" {
		t.Fatalf("create = %#v, err = %v", created, err)
	}
	if _, err := store.CreateTextbook(ctx, TextbookInput{Name: "高等数学", Tags: "同济版", CourseCode: "C1"}, now); !errors.Is(err, ErrTextbookExists) {
		t.Fatalf("duplicate create error = %v, want ErrTextbookExists", err)
	}
	// Editing the book and moving it to another course keeps a single row.
	if err := store.UpdateTextbook(ctx, created.ID, TextbookInput{Name: "高等数学（上）", Tags: "同济版/第7版", CourseCode: "C2"}, now); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTextbook(ctx, created.ID)
	if err != nil || got.Name != "高等数学（上）" || got.CourseCode != "C2" {
		t.Fatalf("updated = %#v, err = %v", got, err)
	}
	if err := store.UpdateTextbook(ctx, "missing", TextbookInput{Name: "x"}, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("updating a missing textbook error = %v, want sql.ErrNoRows", err)
	}
	if err := store.DeleteTextbook(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTextbook(ctx, created.ID); err == nil {
		t.Fatal("deleting a missing textbook should fail")
	}
}
