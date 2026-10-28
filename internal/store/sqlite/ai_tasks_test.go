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
	if err := store.InitSchema(context.Background()); err != nil {
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

	task, inserted, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || task.Status != "queued" || task.NextPage != 1 || task.Solvable != -1 {
		t.Fatalf("new task = %#v inserted=%v", task, inserted)
	}

	same, inserted, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil || inserted || same.ID != task.ID {
		t.Fatalf("active task should be reused: %#v inserted=%v err=%v", same, inserted, err)
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

	// An unfinished run is never duplicated.
	if _, inserted, err := store.CreateAITask(ctx, resourceID, "admin"); err != nil || inserted {
		t.Fatalf("active task should not be duplicated: inserted=%v err=%v", inserted, err)
	}

	// Once the first run finishes, re-analysing keeps it and starts a new row.
	succeeded := "succeeded"
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{Status: &succeeded}); err != nil {
		t.Fatal(err)
	}
	newTask, inserted, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil || !inserted || newTask.ID == task.ID {
		t.Fatalf("re-analysis should create a new row: %#v inserted=%v err=%v", newTask, inserted, err)
	}
	old, err := store.GetAITask(ctx, task.ID)
	if err != nil || old.Status != "succeeded" || old.Markdown != markdown {
		t.Fatalf("previous run was not preserved: %#v err=%v", old, err)
	}

	list, total, err := store.ListAITasks(ctx, "queued", "高数", 10, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != newTask.ID {
		t.Fatalf("list = %#v total=%d err=%v", list, total, err)
	}
	all, err := store.ListAITasksByResource(ctx, resourceID)
	if err != nil || len(all) != 2 {
		t.Fatalf("history = %#v err=%v", all, err)
	}
}

func TestLatestAITaskUsesInsertionOrderWhenTimestampsTie(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	status := "succeeded"
	var newest AITask
	for i := 0; i < 2; i++ {
		task, _, err := store.CreateAITask(ctx, resourceID, "admin")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{Status: &status}); err != nil {
			t.Fatal(err)
		}
		newest = task
	}
	if _, err := store.DB.ExecContext(ctx, "UPDATE resource_ai_tasks SET created_at = 100 WHERE resource_id = ?", resourceID); err != nil {
		t.Fatal(err)
	}
	succeeded, err := store.LatestSucceededAITasks(ctx, []string{resourceID})
	if err != nil || succeeded[resourceID].ID != newest.ID {
		t.Fatalf("latest succeeded = %#v, err = %v", succeeded, err)
	}
	latest, err := store.LatestAITasks(ctx, []string{resourceID})
	if err != nil || latest[resourceID].ID != newest.ID {
		t.Fatalf("latest = %#v, err = %v", latest, err)
	}
	single, ok, err := store.LatestAITaskByResource(ctx, resourceID)
	if err != nil || !ok || single.ID != newest.ID {
		t.Fatalf("latest single = %#v, ok = %v, err = %v", single, ok, err)
	}
}

func TestAITaskZeroFinishedAt(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	task, _, err := store.CreateAITask(ctx, resourceID, "admin")
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

func TestDeleteOlderAITasksKeepsNewest(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	first, _, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	firstKey := "ai/papers/" + resourceID + "/" + first.ID + ".pdf"
	succeeded := "succeeded"
	if err := store.UpdateAITask(ctx, first.ID, AITaskPatch{Status: &succeeded, OutputKey: &firstKey}); err != nil {
		t.Fatal(err)
	}
	second, inserted, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil || !inserted {
		t.Fatalf("second task: %#v inserted=%v err=%v", second, inserted, err)
	}
	secondKey := "ai/papers/" + resourceID + "/" + second.ID + ".pdf"
	if err := store.UpdateAITask(ctx, second.ID, AITaskPatch{Status: &succeeded, OutputKey: &secondKey}); err != nil {
		t.Fatal(err)
	}

	keys, err := store.DeleteOlderAITasksForResource(ctx, resourceID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != firstKey {
		t.Fatalf("output keys = %#v", keys)
	}
	all, err := store.ListAITasksByResource(ctx, resourceID)
	if err != nil || len(all) != 1 || all[0].ID != second.ID {
		t.Fatalf("remaining tasks = %#v err=%v", all, err)
	}
}

func TestCreateAITasksForMissingResources(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	skipped, err := store.CreateResource(ctx, ResourceInput{Name: "跳过.pdf"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, skipped.ID, "published/papers/skip.pdf", now); err != nil {
		t.Fatal(err)
	}
	skippedTask, _, err := store.CreateAITask(ctx, skipped.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	status := "skipped"
	if err := store.UpdateAITask(ctx, skippedTask.ID, AITaskPatch{Status: &status}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateResource(ctx, ResourceInput{Name: "无 PDF.pdf"}, now); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateAITasksForMissingResources(ctx, 10, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].ResourceID != resourceID || created[0].Status != "queued" || created[0].CreatedBy != "auto" {
		t.Fatalf("created = %#v", created)
	}
	again, err := store.CreateAITasksForMissingResources(ctx, 10, "auto")
	if err != nil || len(again) != 0 {
		t.Fatalf("second scan = %#v err=%v", again, err)
	}
	all, err := store.ListAITasksByResource(ctx, skipped.ID)
	if err != nil || len(all) != 1 || all[0].Status != "skipped" {
		t.Fatalf("skipped resource tasks = %#v err=%v", all, err)
	}
}

func TestRequeueFailedAITasks(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()

	task, _, err := store.CreateAITask(ctx, resourceID, "auto")
	if err != nil {
		t.Fatal(err)
	}
	status := "failed"
	reason := "解析失败：402 Insufficient Balance"
	attempt := 2
	finished := time.Now().UTC()
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{
		Status: &status, Error: &reason, Attempt: &attempt, FinishedAt: &finished,
	}); err != nil {
		t.Fatal(err)
	}

	// A fresh failure waits for the cooldown instead of retrying at once.
	if count, err := store.RequeueFailedAITasks(ctx, time.Hour, 10); err != nil || count != 0 {
		t.Fatalf("fresh failure requeued: count=%d err=%v", count, err)
	}
	count, err := store.RequeueFailedAITasks(ctx, 0, 10)
	if err != nil || count != 1 {
		t.Fatalf("requeue: count=%d err=%v", count, err)
	}
	after, err := store.GetAITask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "queued" || after.Error != "" || after.Attempt != 0 || !after.FinishedAt.IsZero() {
		t.Fatalf("after requeue = %#v", after)
	}
	// The queued run must not be requeued again while it waits for the worker.
	if count, err := store.RequeueFailedAITasks(ctx, 0, 10); err != nil || count != 0 {
		t.Fatalf("double requeue: count=%d err=%v", count, err)
	}
}

func TestCreateAITaskReusesActiveRun(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()

	basic, inserted, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil || !inserted {
		t.Fatalf("basic task = %#v inserted=%v err=%v", basic, inserted, err)
	}
	if _, ok, err := store.ClaimNextAITask(ctx); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	// One analysis run produces both reports, so a second request must not start
	// a parallel run for the same paper.
	same, inserted, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil || inserted || same.ID != basic.ID {
		t.Fatalf("second mode = %#v inserted=%v err=%v", same, inserted, err)
	}
	active, ok, err := store.GetActiveAITaskByResource(ctx, resourceID)
	if err != nil || !ok || active.ID != basic.ID {
		t.Fatalf("active = %#v ok=%v err=%v", active, ok, err)
	}
}

func TestDeleteOlderAITasksRemovesPreviousRun(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()

	basic, _, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	basicStatus := "succeeded"
	basicKey := "ai/papers/" + resourceID + "/" + basic.ID + ".pdf"
	if err := store.UpdateAITask(ctx, basic.ID, AITaskPatch{Status: &basicStatus, OutputKey: &basicKey}); err != nil {
		t.Fatal(err)
	}
	more, _, err := store.CreateAITask(ctx, resourceID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := store.DeleteOlderAITasksForResource(ctx, resourceID, more.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != basicKey {
		t.Fatalf("deleted keys = %#v", keys)
	}
	all, err := store.ListAITasksByResource(ctx, resourceID)
	if err != nil || len(all) != 1 || all[0].ID != more.ID {
		t.Fatalf("tasks = %#v err=%v", all, err)
	}
}
