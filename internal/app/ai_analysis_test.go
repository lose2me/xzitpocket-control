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
	a.aiModel = model
	a.aiWindows = []ai.Window{{Start: 0, End: 1440}}
	a.aiLocation = time.UTC
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
	task, err := store.CreateOrResetAITask(ctx, resource.ID, "admin")
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
	if final.OutputKey != aiOutputPrefix+resource.ID+".pdf" || final.OutputSize == 0 {
		t.Fatalf("output = %q size=%d", final.OutputKey, final.OutputSize)
	}
	if !strings.Contains(final.Markdown, "答案") || !sawPrecheck || !sawAnalysis {
		t.Fatalf("markdown=%q precheck=%v analysis=%v", final.Markdown, sawPrecheck, sawAnalysis)
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
