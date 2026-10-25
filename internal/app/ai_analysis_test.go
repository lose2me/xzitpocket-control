package app

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/ai"
	"xzitpocket-control/internal/store/sqlite"
)

// TestRunAITaskPipeline exercises the full worker path with a fake
// OpenAI-compatible endpoint and a stub COS client.
func TestRunAITaskPipeline(t *testing.T) {
	for _, binary := range []string{"pdftohtml", "cwebp", "weasyprint"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s not installed", binary)
		}
	}
	pdf := makeAITestPDF(t)

	var sawPrecheck, sawAnalysis bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		content := `{"choices":[{"index":0,"message":{"role":"assistant","content":"### 第 1 题\n\n**答案：** B\n\n**解析：** 因为 $f'(x)=2x+2$。"},"finish_reason":"stop"}],"model":"deepseek-flash"}`
		if len(body.Messages) > 0 {
			raw, _ := json.Marshal(body.Messages[0].Content)
			if strings.Contains(string(raw), "试卷识别助手") {
				sawPrecheck = true
				content = `{"choices":[{"index":0,"message":{"role":"assistant","content":"{\"solvable\":true,\"reason\":\"完整试卷\",\"subject\":\"数学\",\"question_count\":1}"},"finish_reason":"stop"}],"model":"deepseek-flash"}`
			} else if strings.Contains(string(raw), "答案与解析") {
				sawAnalysis = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(content))
	}))
	defer server.Close()

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
	a.Cfg.AIPagesPerChunk = 1
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
	a.runAITask(ctx, claimed)

	final, err := store.GetAITask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "succeeded" {
		t.Fatalf("status = %s, error = %s", final.Status, final.Error)
	}
	if final.OutputKey != aiOutputPrefix+resource.ID+"/"+task.ID+".pdf" || final.OutputSize == 0 {
		t.Fatalf("output = %q size=%d", final.OutputKey, final.OutputSize)
	}
	if !strings.Contains(final.Markdown, "答案") || !sawPrecheck || !sawAnalysis {
		t.Fatalf("markdown=%q precheck=%v analysis=%v", final.Markdown, sawPrecheck, sawAnalysis)
	}
	if !strings.Contains(final.PromptsJSON, "试卷识别助手") || !strings.Contains(final.PromptsJSON, "答案与解析") {
		t.Fatalf("prompts were not persisted: %.300s", final.PromptsJSON)
	}
	if !strings.Contains(final.PromptsJSON, `"role":"assistant"`) || !strings.Contains(final.PromptsJSON, "solvable") {
		t.Fatalf("precheck answer missing from prompts: %.300s", final.PromptsJSON)
	}
	if strings.Contains(final.MessagesJSON, "请判断它是否是可用于作答的试卷") {
		t.Fatalf("precheck leaked into the analysis conversation: %.300s", final.MessagesJSON)
	}
	row, err := store.GetAITaskRow(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := aiTaskView(row, true, 1)
	if !view.HasPrompts || view.Prompts == "" {
		t.Fatalf("prompt detail missing: has=%v len=%d", view.HasPrompts, len(view.Prompts))
	}
}

func makeAITestPDF(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 160, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: 120, B: uint8(y * 2), A: 255})
		}
	}
	file, err := os.Create(filepath.Join(dir, "fig.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	file.Close()
	html := `<html><head><meta charset="utf-8"><style>@page{size:A4;margin:18mm}body{font-family:"Noto Sans CJK SC";font-size:11pt}</style></head><body>
<h1>高等数学 期末考试试卷</h1>
<p>1. 设函数 f(x)=x^2+2x+1，则 f'(x) 等于（ ）</p>
<p>A. 2x+1　B. 2x+2</p>
<p><img src="fig.png" style="width:50mm"></p>
</body></html>`
	if err := os.WriteFile(filepath.Join(dir, "sample.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("weasyprint", "sample.html", "sample.pdf")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("weasyprint: %v: %s", err, out)
	}
	pdf, err := os.ReadFile(filepath.Join(dir, "sample.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	return pdf
}

func TestEnqueueRestartsActiveAITask(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	a.aiState.Store(&aiRuntime{model: &ai.Model{}, windows: []ai.Window{{Start: 0, End: 1440}}, location: time.UTC})
	now := time.Now().UTC()
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "重解.pdf"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "published/papers/restart.pdf", now); err != nil {
		t.Fatal(err)
	}
	first, inserted, err := store.CreateAITask(ctx, resource.ID, "admin")
	if err != nil || !inserted {
		t.Fatalf("create: %#v inserted=%v err=%v", first, inserted, err)
	}
	claimed, ok, err := store.ClaimNextAITask(ctx)
	if err != nil || !ok || claimed.ID != first.ID {
		t.Fatalf("claim: %#v ok=%v err=%v", claimed, ok, err)
	}

	created, failed, err := a.EnqueueAIAnalyses(ctx, []string{resource.ID}, "admin")
	if err != nil || len(failed) != 0 || len(created) != 1 {
		t.Fatalf("enqueue: created=%#v failed=%#v err=%v", created, failed, err)
	}
	if created[0].ID == first.ID || created[0].Status != "queued" {
		t.Fatalf("new run = %#v", created[0])
	}
	old, err := store.GetAITask(ctx, first.ID)
	if err != nil || old.Status != "cancelled" {
		t.Fatalf("previous run = %#v err=%v", old, err)
	}
	all, err := store.ListAITasksByResource(ctx, resource.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("history = %#v err=%v", all, err)
	}
}

func TestAITaskViewElapsedUsesServerTime(t *testing.T) {
	now := time.Now().UTC()
	queued := sqlite.AITaskRow{AITask: sqlite.AITask{CreatedAt: now.Add(-100 * time.Second), Status: "queued"}}
	if got := aiTaskView(queued, false, 1).ElapsedSeconds; got != 0 {
		t.Fatalf("queued elapsed = %d, want 0", got)
	}
	running := sqlite.AITaskRow{AITask: sqlite.AITask{CreatedAt: now.Add(-100 * time.Second), StartedAt: now.Add(-10 * time.Second), Status: "running"}}
	if got := aiTaskView(running, false, 1).ElapsedSeconds; got < 9 || got > 11 {
		t.Fatalf("running elapsed = %d", got)
	}
	finished := sqlite.AITaskRow{AITask: sqlite.AITask{
		CreatedAt:  now.Add(-100 * time.Second),
		StartedAt:  now.Add(-90 * time.Second),
		FinishedAt: now.Add(-20 * time.Second),
		Status:     "succeeded",
	}}
	if got := aiTaskView(finished, false, 1).ElapsedSeconds; got != 70 {
		t.Fatalf("finished elapsed = %d, want 70", got)
	}
	paused := sqlite.AITaskRow{AITask: sqlite.AITask{
		CreatedAt: now.Add(-100 * time.Second),
		StartedAt: now.Add(-90 * time.Second),
		UpdatedAt: now.Add(-30 * time.Second),
		Status:    "paused",
	}}
	if got := aiTaskView(paused, false, 1).ElapsedSeconds; got != 60 {
		t.Fatalf("paused elapsed = %d, want 60", got)
	}
}
