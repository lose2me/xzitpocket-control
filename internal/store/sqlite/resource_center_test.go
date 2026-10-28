package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newResourceCenterStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestResourceRevisionsTrackOlderEditsAndCourseMoves(t *testing.T) {
	store := newResourceCenterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old, err := store.CreateResource(ctx, ResourceInput{Name: "older.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateResource(ctx, ResourceInput{Name: "newer.pdf", CourseCode: "C1"}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	revision := func(code string) string {
		t.Helper()
		value, err := store.CourseContentRevision(ctx, code)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before, global := revision("C1"), revision("")
	if err := store.SetResourcePdfKey(ctx, old.ID, "published/changed.pdf", now); err != nil {
		t.Fatal(err)
	}
	if revision("C1") == before || revision("") == global {
		t.Fatal("editing a row older than MAX(updated_at) must invalidate caches")
	}
	before, global = revision("C1"), revision("")
	if err := store.IncrementResourceDownloads(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	if revision("C1") != before || revision("") != global {
		t.Fatal("download counters must not invalidate public content caches")
	}
	targetBefore := revision("C2")
	if _, err := store.DB.ExecContext(ctx, "UPDATE resources SET course_code = ? WHERE id = ?", "C2", old.ID); err != nil {
		t.Fatal(err)
	}
	if revision("C1") == before || revision("C2") == targetBefore {
		t.Fatal("moving content must invalidate both courses")
	}
	before, global = revision("C2"), revision("")
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE id = ?", old.ID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if revision("C2") != before || revision("") != global {
		t.Fatal("rolled-back content must not change revisions")
	}
}

func TestResourceCoverUsesFirstAvailableCoverAndItsOwnVersion(t *testing.T) {
	store := newResourceCenterStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := store.CreateCourse(ctx, CourseInput{Code: "C1", Name: "course"}, now); err != nil {
		t.Fatal(err)
	}
	beforeUpload, err := store.ResourceCenterRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UploadCourses(ctx, []CourseInput{{Code: "C1", Name: "course"}}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	afterUpload, err := store.ResourceCenterRevision(ctx)
	if err != nil || afterUpload != beforeUpload {
		t.Fatalf("unchanged foreground upload invalidated caches: %s -> %s, err %v", beforeUpload, afterUpload, err)
	}
	if _, err := store.CreateTextbook(ctx, TextbookInput{Name: "A without cover", CourseCodes: []string{"C1"}}, now); err != nil {
		t.Fatal(err)
	}
	book, err := store.CreateTextbook(ctx, TextbookInput{Name: "B with cover", CourseCodes: []string{"C1", "C2"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookCoverKey(ctx, book.ID, "covers/first.webp", now); err != nil {
		t.Fatal(err)
	}
	shared, err := store.CreateTextbook(ctx, TextbookInput{Name: "Shared cover", CourseCodes: []string{"C3"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookCoverKey(ctx, shared.ID, "covers/first.webp", now); err != nil {
		t.Fatal(err)
	}
	readCourse := func() ResourceCourse {
		t.Helper()
		courses, err := store.ListResourceCourses(ctx)
		if err != nil || len(courses) != 1 {
			t.Fatalf("courses = %#v, err = %v", courses, err)
		}
		return courses[0]
	}
	first := readCourse()
	if first.CoverTextbookID != book.ID || first.CoverVersion == "" {
		t.Fatalf("cover = %#v", first)
	}
	if _, err := store.CreateResource(ctx, ResourceInput{Name: "paper.pdf", CourseCode: "C1"}, now); err != nil {
		t.Fatal(err)
	}
	second := readCourse()
	if second.Version == first.Version || second.CoverVersion != first.CoverVersion {
		t.Fatal("adding a paper must change content version and retain cover version")
	}
	sharedBefore, err := store.CourseContentRevision(ctx, "C3")
	if err != nil {
		t.Fatal(err)
	}
	// Uploading another cover for the same PDF overwrites its existing key.
	if err := store.SetTextbookCoverKey(ctx, book.ID, "covers/first.webp", now); err != nil {
		t.Fatal(err)
	}
	if readCourse().CoverVersion == first.CoverVersion {
		t.Fatal("replacing the cover must invalidate its file cache")
	}
	sharedAfter, err := store.CourseContentRevision(ctx, "C3")
	if err != nil || sharedAfter == sharedBefore {
		t.Fatalf("overwriting a shared cover must invalidate its other courses: %s -> %s, err %v", sharedBefore, sharedAfter, err)
	}
	before, err := store.CourseContentRevision(ctx, "C2")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTextbook(ctx, book.ID); err != nil {
		t.Fatal(err)
	}
	after, err := store.CourseContentRevision(ctx, "C2")
	if err != nil || before == after || readCourse().CoverTextbookID != "" {
		t.Fatalf("cascaded association deletion did not invalidate courses: %s -> %s, err %v", before, after, err)
	}
}

func TestResourceRevisionOnlyTracksPublishedAIResults(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	revision := func() string {
		t.Helper()
		value, err := store.CourseContentRevision(ctx, "C1")
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := revision()
	task, _, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	running, succeeded := "running", "succeeded"
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{Status: &running}); err != nil {
		t.Fatal(err)
	}
	if revision() != before {
		t.Fatal("unpublished task progress must not invalidate app caches")
	}
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{Status: &succeeded}); err != nil {
		t.Fatal(err)
	}
	if revision() == before {
		t.Fatal("publishing AI results must invalidate course content")
	}
	before = revision()
	if _, err := store.DB.ExecContext(ctx, "DELETE FROM resource_ai_tasks WHERE id = ?", task.ID); err != nil {
		t.Fatal(err)
	}
	if revision() == before {
		t.Fatal("removing published AI results must invalidate course content")
	}
}
