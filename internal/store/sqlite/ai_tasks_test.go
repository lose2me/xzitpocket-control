package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newAITestStore(t *testing.T) (*Store, string) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	resource, err := store.CreateResource(ctx, ResourceInput{Name: "高数.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "published/papers/abc.pdf", now); err != nil {
		t.Fatal(err)
	}
	return store, resource.ID
}

func TestAITaskLifecycle(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()

	task, err := store.CreateOrResetAITask(ctx, resourceID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "queued" || task.NextPage != 1 || task.Solvable != -1 {
		t.Fatalf("new task = %#v", task)
	}

	claimed, ok, err := store.ClaimNextAITask(ctx)
	if err != nil || !ok || claimed.ID != task.ID || claimed.Status != "running" {
		t.Fatalf("claim = %#v, ok=%v, err=%v", claimed, ok, err)
	}
	if _, ok, err := store.ClaimNextAITask(ctx); err != nil || ok {
		t.Fatalf("second claim should be empty, ok=%v err=%v", ok, err)
	}

	next := 3
	markdown := "### 第 1 题\n答案：A"
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{NextPage: &next, Markdown: &markdown}); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetAITaskRow(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.NextPage != 3 || row.Markdown != markdown || row.ResourceName != "高数.pdf" {
		t.Fatalf("row = %#v", row)
	}

	count, err := store.ResetRunningAITasks(ctx)
	if err != nil || count != 1 {
		t.Fatalf("reset count=%d err=%v", count, err)
	}
	reloaded, err := store.GetAITask(ctx, task.ID)
	if err != nil || reloaded.Status != "queued" {
		t.Fatalf("reloaded = %#v err=%v", reloaded, err)
	}

	// Re-enqueueing an existing task resets progress.
	if _, err := store.CreateOrResetAITask(ctx, resourceID, "admin"); err != nil {
		t.Fatal(err)
	}
	reloaded, _ = store.GetAITask(ctx, task.ID)
	if reloaded.NextPage != 1 || reloaded.Markdown != "" {
		t.Fatalf("reset task = %#v", reloaded)
	}

	list, total, err := store.ListAITasks(ctx, "queued", "高数", 10, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list = %#v total=%d err=%v", list, total, err)
	}
}

func TestAITaskZeroFinishedAt(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	task, err := store.CreateOrResetAITask(ctx, resourceID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{FinishedAt: &now}); err != nil {
		t.Fatal(err)
	}
	zero := time.Time{}
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{FinishedAt: &zero}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.GetAITask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.FinishedAt.IsZero() {
		t.Fatalf("finished_at = %v", reloaded.FinishedAt)
	}
}
