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

	task, inserted, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || task.Status != "queued" || task.NextPage != 1 || task.Solvable != -1 {
		t.Fatalf("new task = %#v inserted=%v", task, inserted)
	}

	same, inserted, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
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
	if _, inserted, err := store.CreateAITask(ctx, resourceID, "basic", "admin"); err != nil || inserted {
		t.Fatalf("active task should not be duplicated: inserted=%v err=%v", inserted, err)
	}

	// Once the first run finishes, re-analysing keeps it and starts a new row.
	succeeded := "succeeded"
	if err := store.UpdateAITask(ctx, task.ID, AITaskPatch{Status: &succeeded}); err != nil {
		t.Fatal(err)
	}
	newTask, inserted, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
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

func TestAITaskZeroFinishedAt(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	task, _, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
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

func TestAllowMultipleAITasksPerResource(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.DB.ExecContext(ctx, `CREATE TABLE resources (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `CREATE TABLE resource_ai_tasks (
		id TEXT PRIMARY KEY,
		resource_id TEXT NOT NULL UNIQUE REFERENCES resources(id) ON DELETE CASCADE,
		mode TEXT NOT NULL DEFAULT 'basic',
		status TEXT NOT NULL DEFAULT 'queued',
		solvable INTEGER NOT NULL DEFAULT -1,
		precheck_note TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL DEFAULT 0,
		next_page INTEGER NOT NULL DEFAULT 1,
		total_pages INTEGER NOT NULL DEFAULT 0,
		messages_json TEXT NOT NULL DEFAULT '[]',
		markdown TEXT NOT NULL DEFAULT '',
		output_key TEXT NOT NULL DEFAULT '',
		output_size INTEGER NOT NULL DEFAULT 0,
		more_markdown TEXT NOT NULL DEFAULT '',
		more_output_key TEXT NOT NULL DEFAULT '',
		more_output_size INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		started_at INTEGER NOT NULL DEFAULT 0,
		finished_at INTEGER NOT NULL DEFAULT 0,
		prompts_json TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO resources(id) VALUES ('res_1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO resource_ai_tasks(id, resource_id, status, created_at, updated_at)
		VALUES ('ai_old', 'res_1', 'succeeded', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := store.allowMultipleAITasksPerResource(ctx); err != nil {
		t.Fatalf("allow multiple AI tasks: %v", err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO resource_ai_tasks(id, resource_id, status, created_at, updated_at)
		VALUES ('ai_new', 'res_1', 'queued', 2, 2)`); err != nil {
		t.Fatalf("second task for the same resource: %v", err)
	}
	var count int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM resource_ai_tasks WHERE resource_id = 'res_1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("task count = %d, want 2", count)
	}
}

func TestDeleteOlderAITasksKeepsNewest(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	first, _, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
	if err != nil {
		t.Fatal(err)
	}
	firstKey := "ai/papers/" + resourceID + "/" + first.ID + ".pdf"
	succeeded := "succeeded"
	if err := store.UpdateAITask(ctx, first.ID, AITaskPatch{Status: &succeeded, OutputKey: &firstKey}); err != nil {
		t.Fatal(err)
	}
	second, inserted, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
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
	skippedTask, _, err := store.CreateAITask(ctx, skipped.ID, "basic", "admin")
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

	task, _, err := store.CreateAITask(ctx, resourceID, "basic", "auto")
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

func TestCreateAITaskReusesActiveRunAcrossModes(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()

	basic, inserted, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
	if err != nil || !inserted {
		t.Fatalf("basic task = %#v inserted=%v err=%v", basic, inserted, err)
	}
	if _, ok, err := store.ClaimNextAITask(ctx); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	// One analysis run produces both reports, so a second mode must not start
	// a parallel run for the same paper.
	same, inserted, err := store.CreateAITask(ctx, resourceID, "more", "admin")
	if err != nil || inserted || same.ID != basic.ID {
		t.Fatalf("second mode = %#v inserted=%v err=%v", same, inserted, err)
	}
	active, ok, err := store.GetActiveAITaskByResource(ctx, resourceID, "")
	if err != nil || !ok || active.ID != basic.ID {
		t.Fatalf("active = %#v ok=%v err=%v", active, ok, err)
	}
}

func TestDeleteOlderAITasksRemovesLegacyOtherModes(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()

	basic, _, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
	if err != nil {
		t.Fatal(err)
	}
	basicStatus := "succeeded"
	basicKey := "ai/papers/" + resourceID + "/" + basic.ID + ".pdf"
	if err := store.UpdateAITask(ctx, basic.ID, AITaskPatch{Status: &basicStatus, OutputKey: &basicKey}); err != nil {
		t.Fatal(err)
	}
	more, _, err := store.CreateAITask(ctx, resourceID, "more", "admin")
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

func TestAllowMultipleAITasksRenamesLegacyAdvancedMode(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.DB.ExecContext(ctx, `CREATE TABLE resources (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `CREATE TABLE resource_ai_tasks (
		id TEXT PRIMARY KEY,
		resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
		mode TEXT NOT NULL DEFAULT 'basic' CHECK (mode IN ('basic', 'advanced')),
		status TEXT NOT NULL DEFAULT 'queued',
		solvable INTEGER NOT NULL DEFAULT -1,
		precheck_note TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL DEFAULT 0,
		next_page INTEGER NOT NULL DEFAULT 1,
		total_pages INTEGER NOT NULL DEFAULT 0,
		messages_json TEXT NOT NULL DEFAULT '[]',
		markdown TEXT NOT NULL DEFAULT '',
		output_key TEXT NOT NULL DEFAULT '',
		output_size INTEGER NOT NULL DEFAULT 0,
		more_markdown TEXT NOT NULL DEFAULT '',
		more_output_key TEXT NOT NULL DEFAULT '',
		more_output_size INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		started_at INTEGER NOT NULL DEFAULT 0,
		finished_at INTEGER NOT NULL DEFAULT 0,
		prompts_json TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO resources(id) VALUES ('res_1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO resource_ai_tasks(id, resource_id, mode, status, created_at, updated_at)
		VALUES ('ai_old', 'res_1', 'advanced', 'succeeded', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := store.allowMultipleAITasksPerResource(ctx); err != nil {
		t.Fatalf("migrate legacy advanced mode: %v", err)
	}
	var mode string
	if err := store.DB.QueryRowContext(ctx, `SELECT mode FROM resource_ai_tasks WHERE id = 'ai_old'`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "more" {
		t.Fatalf("mode = %q, want more", mode)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO resource_ai_tasks(id, resource_id, mode, status, created_at, updated_at)
		VALUES ('ai_more', 'res_1', 'more', 'succeeded', 2, 2)`); err != nil {
		t.Fatalf("insert more task after migration: %v", err)
	}
}

func TestMigrateAdvancedAIModeWithoutCheckConstraint(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.DB.ExecContext(ctx, `CREATE TABLE resource_ai_tasks (
		id TEXT PRIMARY KEY,
		resource_id TEXT NOT NULL,
		mode TEXT NOT NULL DEFAULT 'basic',
		status TEXT NOT NULL DEFAULT 'queued',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO resource_ai_tasks(id, resource_id, mode, status, created_at, updated_at) VALUES
		('ai_done', 'res_1', 'advanced', 'succeeded', 1, 1),
		('ai_active', 'res_2', 'advanced', 'paused', 2, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateAdvancedAIMode(ctx); err != nil {
		t.Fatal(err)
	}
	var advanced int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM resource_ai_tasks WHERE mode = 'advanced'`).Scan(&advanced); err != nil {
		t.Fatal(err)
	}
	if advanced != 0 {
		t.Fatalf("legacy advanced rows = %d, want 0", advanced)
	}
}

func TestMergeLegacyAIReports(t *testing.T) {
	store, resourceID := newAITestStore(t)
	ctx := context.Background()
	basic, _, err := store.CreateAITask(ctx, resourceID, "basic", "admin")
	if err != nil {
		t.Fatal(err)
	}
	basicStatus := "succeeded"
	basicKey := "ai/papers/" + resourceID + "/" + basic.ID + ".pdf"
	basicMarkdown := "# 精简版"
	if err := store.UpdateAITask(ctx, basic.ID, AITaskPatch{
		Status: &basicStatus, OutputKey: &basicKey, Markdown: &basicMarkdown,
	}); err != nil {
		t.Fatal(err)
	}
	more, _, err := store.CreateAITask(ctx, resourceID, "more", "admin")
	if err != nil {
		t.Fatal(err)
	}
	moreKey := "ai/papers/" + resourceID + "/" + more.ID + ".pdf"
	moreMarkdown := "# 小学生版"
	moreSize := int64(456)
	if err := store.UpdateAITask(ctx, more.ID, AITaskPatch{
		Status: &basicStatus, OutputKey: &moreKey, OutputSize: &moreSize, Markdown: &moreMarkdown,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.mergeLegacyAIReports(ctx); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListAITasksByResource(ctx, resourceID)
	if err != nil || len(all) != 1 {
		t.Fatalf("tasks = %#v err=%v", all, err)
	}
	got := all[0]
	if got.ID != basic.ID || got.MoreOutputKey != moreKey || got.MoreOutputSize != moreSize || got.MoreMarkdown != moreMarkdown {
		t.Fatalf("merged task = %#v", got)
	}
}
