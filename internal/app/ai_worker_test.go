package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/ai"
	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/store/sqlite"
)

func TestStartAIAnalysisProcessesQueuedTask(t *testing.T) {
	handler := modelHandler(true, "完整试卷", 200, chatResponse("<!-- BASIC -->\n### 第 1 题\n\n**答案：** B\n\n<!-- MORE -->\n### 第 1 题\n\n**考点：** 导数", "stop"))
	p := newAIPipeline(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.app.StartAIAnalysis(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	deadline := time.Now().Add(40 * time.Second)
	for {
		final, err := p.store.GetAITask(context.Background(), p.task.ID)
		if err == nil && final.Status == "succeeded" {
			if final.OutputKey == "" || final.Markdown == "" {
				t.Fatalf("worker output = %+v", final)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not finish the task: %+v %v", final, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("StartAIAnalysis did not stop")
	}

	// The auto-scan queues papers that have no task yet.
	now := time.Now().UTC()
	extra, err := p.store.CreateResource(context.Background(), sqlite.ResourceInput{Name: "自动.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.store.SetResourcePdfKey(context.Background(), extra.ID, "published/papers/auto.pdf", now); err != nil {
		t.Fatal(err)
	}
	tasks, _, err := p.app.ListAITasksForAdmin(context.Background(), "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		t.Fatal("expected the completed task to be listed")
	}
}

func TestPrecheckRetryStopsWhenCancelled(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(readBody(r), "试卷识别助手") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"provider down","type":"server_error"}}`))
			return
		}
		_, _ = w.Write([]byte(chatResponse("x", "stop")))
	}
	p := newAIPipeline(t, handler)
	p.app.Cfg.AIMaxAttempts = 3
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	p.app.runAITask(ctx, p.claimed)
	final := p.final(t)
	if final.Status != "paused" || !strings.Contains(final.Error, "等待下一个解析时间窗") {
		t.Fatalf("cancelled retry = %s %q", final.Status, final.Error)
	}
	if final.PromptsJSON == "" {
		t.Fatal("failed precheck prompts should be stored")
	}
}

func readBody(r *http.Request) string {
	raw := make([]byte, 4096)
	n, _ := r.Body.Read(raw)
	return string(raw[:n])
}

func TestCleanupOlderAITasksRemovesOutputs(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	ctx := context.Background()
	if err := p.store.UpdateAITask(ctx, p.task.ID, sqlite.AITaskPatch{
		Status: stringPointer("succeeded"), OutputKey: stringPointer("ai/papers/old.pdf"),
		MoreOutputKey: stringPointer("ai/papers/old-more.pdf"),
	}); err != nil {
		t.Fatal(err)
	}
	newer, _, err := p.store.CreateAITask(ctx, p.resource.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.store.UpdateAITask(ctx, newer.ID, sqlite.AITaskPatch{Status: stringPointer("succeeded")}); err != nil {
		t.Fatal(err)
	}
	p.app.cosClientOverride = newStubCOSClient(nil, nil)
	p.app.cleanupOlderAITasks(p.resource.ID, newer.ID)
	if _, err := p.store.GetAITask(ctx, p.task.ID); err == nil {
		t.Fatal("older task should be removed")
	}
	// With storage unavailable the rows are still removed and the missing
	// client only produces a warning.
	if err := p.store.UpdateAITask(ctx, newer.ID, sqlite.AITaskPatch{Status: stringPointer("succeeded"), OutputKey: stringPointer("ai/papers/new.pdf")}); err != nil {
		t.Fatal(err)
	}
	third, _, err := p.store.CreateAITask(ctx, p.resource.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.store.UpdateAITask(ctx, third.ID, sqlite.AITaskPatch{Status: stringPointer("succeeded")}); err != nil {
		t.Fatal(err)
	}
	cfg := p.app.cfg()
	cfg.COSSecretID, cfg.COSSecretKey = "", ""
	p.app.SetConfigForTesting(cfg)
	p.app.cleanupOlderAITasks(p.resource.ID, third.ID)
	if _, err := p.store.GetAITask(ctx, newer.ID); err == nil {
		t.Fatal("older task should be removed without storage too")
	}
	// No older tasks is a no-op.
	p.app.cleanupOlderAITasks(p.resource.ID, third.ID)
}

func TestAIInstructionFallbacks(t *testing.T) {
	blank := config.Config{}
	if analysisInstruction(blank) != ai.DefaultAnalysisInstruction {
		t.Fatal("blank analysis instruction should fall back")
	}
	if precheckInstruction(blank) != ai.DefaultPrecheckInstruction {
		t.Fatal("blank precheck instruction should fall back")
	}
	custom := config.Config{AIAnalysisInstruction: "  自定义解析  ", AIPrecheckInstruction: " 自定义预判 "}
	if analysisInstruction(custom) != "  自定义解析  " || precheckInstruction(custom) != " 自定义预判 " {
		t.Fatal("custom instructions should be used verbatim")
	}
}

func TestSaveAIPromptsErrorsAreLogged(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	// Saving to a missing task only logs; it must not panic.
	prompts := ""
	p.app.saveAIPrompts("missing-task", &prompts, []ai.PromptCall{{Kind: "analyze"}})
	if !strings.Contains(prompts, "analyze") {
		t.Fatalf("prompts = %q", prompts)
	}
	empty := ""
	p.app.saveAIPrompts(p.task.ID, &empty, nil)
	if empty != "" {
		t.Fatalf("empty calls should not write: %q", empty)
	}
}

func TestListAITasksForAdminFilters(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	ctx := context.Background()
	items, total, err := p.app.ListAITasksForAdmin(ctx, "running", "高数", 10, 0)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("filtered tasks = %v %d %v", items, total, err)
	}
	items, total, err = p.app.ListAITasksForAdmin(ctx, "succeeded", "", 10, 0)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("empty filter = %v %d %v", items, total, err)
	}
	if _, err := p.app.GetAITaskForAdmin(ctx, "missing"); err == nil {
		t.Fatal("missing ai task should fail")
	}
	view, err := p.app.GetAITaskForAdmin(ctx, p.task.ID)
	if err != nil || view.ID != p.task.ID {
		t.Fatalf("task detail = %+v %v", view, err)
	}
}

func TestRecordingWithClosedStoreIsBestEffort(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	// Closing the store makes the background helpers log instead of crash.
	if err := p.store.Close(); err != nil {
		t.Fatal(err)
	}
	p.app.cleanupOlderAITasks(p.resource.ID, p.task.ID)
	p.app.cleanupAIWorkDirs(context.Background())
	_, _, _ = p.app.ListAITasksForAdmin(context.Background(), "", "", 10, 0)
}
