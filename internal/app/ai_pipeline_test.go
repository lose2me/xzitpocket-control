package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/ai"
	"xzitpocket-control/internal/cloud"
	"xzitpocket-control/internal/store/sqlite"
)

type aiPipeline struct {
	app      *App
	store    *sqlite.Store
	ctx      context.Context
	resource sqlite.Resource
	task     sqlite.AITask
	claimed  sqlite.AITask
}

// newAIPipeline prepares a real PDF-extraction + fake-model run.
func newAIPipeline(t *testing.T, handler http.HandlerFunc) *aiPipeline {
	t.Helper()
	for _, binary := range []string{"pdftohtml", "cwebp", "weasyprint", "node"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s not installed", binary)
		}
	}
	pdf := makeAITestPDF(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	model, err := ai.NewModel(ai.ModelOptions{APIKey: "test-key", BaseURL: server.URL + "/v1", Name: "deepseek-flash"})
	if err != nil {
		t.Fatal(err)
	}
	a, store := newCourseApp(t)
	ctx := context.Background()
	a.cosClientOverride = newStubCOSClient(map[string]string{http.MethodGet: string(pdf)}, nil)
	a.aiState.Store(&aiRuntime{model: model, windows: []ai.Window{{Start: 0, End: 1440}}, location: time.UTC})
	a.aiCancel = make(map[string]context.CancelFunc)
	a.Cfg.AIWorkDir = t.TempDir()
	a.Cfg.AIMaxAttempts = 2
	a.Cfg.AIRequestTimeout = time.Minute
	mathDir := filepath.Join("..", "..", "ai-math")
	if _, err := os.Stat(filepath.Join(mathDir, "node_modules", "mathjax-full")); err == nil {
		a.Cfg.AIMathJaxDir = mathDir
	}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "高数期末.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "published/papers/abc.pdf", now); err != nil {
		t.Fatal(err)
	}
	task, _, err := store.CreateAITask(ctx, resource.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextAITask(ctx)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	return &aiPipeline{app: a, store: store, ctx: ctx, resource: resource, task: task, claimed: claimed}
}

func (p *aiPipeline) final(t *testing.T) sqlite.AITask {
	t.Helper()
	final, err := p.store.GetAITask(p.ctx, p.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func failingCOS() *cloud.COSClient {
	client := cloud.NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: cosRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return cosStubResponse(http.StatusForbidden, "denied", 0), nil
	})}
	return client
}

func chatResponse(text, finish string) string {
	raw, _ := json.Marshal(map[string]any{
		"id": "chat", "object": "chat.completion", "created": 1, "model": "deepseek-flash",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": finish,
		}},
	})
	return string(raw)
}

// modelHandler replies to precheck and analysis calls with the supplied
// verdict and analysis status.
func modelHandler(precheckSolvable bool, precheckReason string, analysisStatus int, analysisBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(raw), "试卷识别助手") {
			_, _ = w.Write([]byte(chatResponse(`{"solvable":`+boolText(precheckSolvable)+`,"reason":"`+precheckReason+`","subject":"数学","question_count":1}`, "stop")))
			return
		}
		if analysisStatus >= 300 {
			w.WriteHeader(analysisStatus)
			_, _ = w.Write([]byte(analysisBody))
			return
		}
		_, _ = w.Write([]byte(analysisBody))
	}
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestAITaskMarksUnsolvablePaperSkipped(t *testing.T) {
	p := newAIPipeline(t, modelHandler(false, "不是试卷", 200, ""))
	p.app.runAITask(p.ctx, p.claimed)
	final := p.final(t)
	if final.Status != "skipped" || final.Solvable != 0 || !strings.Contains(final.Error, "不是试卷") {
		t.Fatalf("skipped task = %s %d %q", final.Status, final.Solvable, final.Error)
	}
	if final.TotalPages == 0 || final.Markdown != "" {
		t.Fatalf("skipped task pages=%d markdown=%q", final.TotalPages, final.Markdown)
	}
	if _, err := os.Stat(filepath.Join(p.app.Cfg.AIWorkDir, p.task.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skipped task left its work directory: %v", err)
	}
}

func TestAITaskFailsOnPermanentAnalysisError(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", http.StatusUnprocessableEntity, `{"error":{"message":"unsupported image","type":"invalid_request_error"}}`))
	p.app.runAITask(p.ctx, p.claimed)
	final := p.final(t)
	if final.Status != "failed" || !strings.Contains(final.Error, "解析失败") {
		t.Fatalf("permanent failure = %s %q", final.Status, final.Error)
	}
	if final.FinishedAt.IsZero() {
		t.Fatal("failed task should be finished")
	}
}

func TestAITaskRetriesTransientAnalysisError(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", http.StatusInternalServerError, `{"error":{"message":"provider down","type":"server_error"}}`))
	p.app.Cfg.AIMaxAttempts = 1
	p.app.runAITask(p.ctx, p.claimed)
	final := p.final(t)
	if final.Status != "failed" || !strings.Contains(final.Error, "已重试 1 次") {
		t.Fatalf("retry failure = %s %q", final.Status, final.Error)
	}
}

func TestAITaskFailsWithoutPdfOrResource(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	// Remove the PDF key: the worker should fail before downloading anything.
	if err := p.store.SetResourcePdfKey(p.ctx, p.resource.ID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	p.app.runAITask(p.ctx, p.claimed)
	if final := p.final(t); final.Status != "failed" || !strings.Contains(final.Error, "没有 PDF") {
		t.Fatalf("missing pdf = %s %q", final.Status, final.Error)
	}

	other := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	other.app.cosClientOverride = failingCOS()
	if _, err := other.store.DB.ExecContext(other.ctx, "DELETE FROM resources"); err != nil {
		t.Fatal(err)
	}
	other.app.runAITask(other.ctx, other.claimed)
	// Deleting the resource cascades to its task row; the worker must not
	// create a work directory for a resource that no longer exists.
	if _, err := other.store.GetAITask(other.ctx, other.task.ID); err == nil {
		t.Fatal("task row should be gone with its resource")
	}
	if _, err := os.Stat(filepath.Join(other.app.Cfg.AIWorkDir, other.task.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing resource left a work directory: %v", err)
	}
}

func TestAITaskPausesWhenDownloadFails(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	p.app.cosClientOverride = failingCOS()
	p.app.runAITask(p.ctx, p.claimed)
	final := p.final(t)
	if final.Status != "paused" || !strings.Contains(final.Error, "下载 PDF 失败") {
		t.Fatalf("download failure = %s %q", final.Status, final.Error)
	}
	if _, err := os.Stat(filepath.Join(p.app.Cfg.AIWorkDir, p.task.ID)); err != nil {
		t.Fatalf("paused task should keep its work directory: %v", err)
	}
}

func TestAITaskStopsWhenCancelledDuringDownload(t *testing.T) {
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	p.app.cosClientOverride = failingCOS()
	if err := p.store.UpdateAITask(p.ctx, p.task.ID, sqlite.AITaskPatch{Status: stringPointer("cancelled")}); err != nil {
		t.Fatal(err)
	}
	p.app.runAITask(p.ctx, p.claimed)
	final := p.final(t)
	if final.Status != "cancelled" {
		t.Fatalf("cancelled task = %s %q", final.Status, final.Error)
	}
}

func TestAITaskFailsWhenExtractionToolsAreMissing(t *testing.T) {
	if _, err := exec.LookPath("pdftohtml"); err != nil {
		t.Skip("pdftohtml not installed")
	}
	p := newAIPipeline(t, modelHandler(true, "完整试卷", 200, chatResponse("x", "stop")))
	t.Setenv("PATH", t.TempDir())
	p.app.runAITask(p.ctx, p.claimed)
	final := p.final(t)
	if final.Status != "failed" || !strings.Contains(final.Error, "解析 PDF 失败") {
		t.Fatalf("missing tools = %s %q", final.Status, final.Error)
	}
	if _, err := os.Stat(filepath.Join(p.app.Cfg.AIWorkDir, p.task.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed task left its work directory: %v", err)
	}
}
