package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

type chatReply struct {
	content string
	finish  string
	status  int
}

func newChatServer(t *testing.T, replies ...chatReply) (*httptest.Server, *[]string) {
	t.Helper()
	requests := &[]string{}
	index := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		*requests = append(*requests, string(raw))
		reply := chatReply{content: "ok", finish: "stop"}
		if index < len(replies) {
			reply = replies[index]
		}
		index++
		if reply.status != 0 && reply.status >= 300 {
			w.WriteHeader(reply.status)
			_, _ = w.Write([]byte(`{"error":{"message":"denied","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chat", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": reply.content},
				"finish_reason": reply.finish,
			}},
		})
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func newModelForTest(t *testing.T, baseURL string) *Model {
	t.Helper()
	model, err := NewModel(ModelOptions{APIKey: "test-key", BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestNewModelDefaultsAndValidation(t *testing.T) {
	if _, err := NewModel(ModelOptions{APIKey: "  "}); err == nil {
		t.Fatal("empty api key should fail")
	}
	model, err := NewModel(ModelOptions{APIKey: "key", BaseURL: "http://example.test/v1/", Name: " custom ", PrecheckSystem: "  "})
	if err != nil {
		t.Fatal(err)
	}
	if model.Name() != "custom" {
		t.Fatalf("name = %q", model.Name())
	}
	if model.precheckSystem != DefaultPrecheckSystem || model.analysisSystem != DefaultAnalysisSystem {
		t.Fatal("blank prompts should fall back to the built-in templates")
	}
	model, err = NewModel(ModelOptions{APIKey: "key", BaseURL: "", Name: "", PrecheckSystem: "p", AnalysisSystem: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if model.Name() != "deepseek-flash" || model.precheckSystem != "p" || model.analysisSystem != "a" {
		t.Fatalf("defaults = %+v", model)
	}
	if promptOr(" ", "fallback") != "fallback" || promptOr("value", "fallback") != "value" {
		t.Fatal("promptOr mismatch")
	}
}

func TestPrecheckParsesVerdictAndRecordsPromptCalls(t *testing.T) {
	server, requests := newChatServer(t, chatReply{content: "```json\n{\"solvable\":true,\"reason\":\"ok\",\"subject\":\"数学\",\"question_count\":20}\n```"})
	model := newModelForTest(t, server.URL+"/v1")
	parts := []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: "第一页"}, {Type: openai.ChatMessagePartTypeImageURL}}
	verdict, content, calls, err := model.Precheck(context.Background(), parts)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Solvable || verdict.Subject != "数学" || verdict.QuestionCount != 20 || !strings.Contains(content, "solvable") {
		t.Fatalf("verdict = %+v content=%q", verdict, content)
	}
	if len(calls) != 1 || calls[0].Kind != "precheck" || calls[0].Messages[0].Content != DefaultPrecheckSystem {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Messages[1].Content != "第一页[图片]" {
		t.Fatalf("image marker = %q", calls[0].Messages[1].Content)
	}
	if len(calls[0].Messages) != 3 || calls[0].Messages[2].Role != "assistant" {
		t.Fatalf("prompt call should record the answer: %+v", calls[0].Messages)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %v", *requests)
	}
}

func TestPrecheckFailures(t *testing.T) {
	ctx := context.Background()
	parts := []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: "x"}}

	server, _ := newChatServer(t, chatReply{content: "not json"})
	if _, content, calls, err := newModelForTest(t, server.URL+"/v1").Precheck(ctx, parts); err == nil || content != "not json" || len(calls) != 1 {
		t.Fatalf("invalid verdict = %q %v", content, err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","choices":[]}`))
	}))
	t.Cleanup(empty.Close)
	if _, _, _, err := newModelForTest(t, empty.URL+"/v1").Precheck(ctx, parts); err == nil {
		t.Fatal("missing choices should fail")
	}

	denied, _ := newChatServer(t, chatReply{status: 401})
	_, _, _, err := newModelForTest(t, denied.URL+"/v1").Precheck(ctx, parts)
	if err == nil || !IsPermanentError(err) {
		t.Fatalf("401 should be permanent: %v", err)
	}

	rateLimited, _ := newChatServer(t, chatReply{status: 429})
	_, _, _, err = newModelForTest(t, rateLimited.URL+"/v1").Precheck(ctx, parts)
	if err == nil || IsPermanentError(err) {
		t.Fatalf("429 should be retryable: %v", err)
	}

	if IsPermanentError(context.Canceled) {
		t.Fatal("non-API errors are not permanent")
	}
}

func TestAnalyzeContinuesTruncatedAnswers(t *testing.T) {
	server, requests := newChatServer(t,
		chatReply{content: "第一部分", finish: "length"},
		chatReply{content: "第二部分", finish: "stop"},
	)
	model := newModelForTest(t, server.URL+"/v1")
	history := []Message{{Role: "assistant", Text: "历史回答"}, {Role: "user", Text: "历史提问"}}
	output, calls, err := model.Analyze(context.Background(), history, []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: "试卷"}})
	if err != nil {
		t.Fatal(err)
	}
	if output != "第一部分第二部分" {
		t.Fatalf("output = %q", output)
	}
	if len(calls) != 2 || calls[0].Kind != "analyze" || calls[1].Kind != "continue" {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Messages[1].Role != "assistant" || calls[0].Messages[2].Role != "user" {
		t.Fatalf("history roles = %+v", calls[0].Messages)
	}
	if len(*requests) != 2 {
		t.Fatalf("requests = %v", *requests)
	}
}

func TestAnalyzeGivesUpAfterMaxContinues(t *testing.T) {
	replies := make([]chatReply, maxAnalysisContinues)
	for i := range replies {
		replies[i] = chatReply{content: "片段", finish: "length"}
	}
	server, _ := newChatServer(t, replies...)
	_, calls, err := newModelForTest(t, server.URL+"/v1").Analyze(context.Background(), nil, nil)
	if err != ErrResponseTruncated {
		t.Fatalf("error = %v", err)
	}
	if len(calls) != maxAnalysisContinues {
		t.Fatalf("calls = %d", len(calls))
	}
}

func TestAnalyzeErrors(t *testing.T) {
	ctx := context.Background()
	denied, _ := newChatServer(t, chatReply{status: 500})
	if _, calls, err := newModelForTest(t, denied.URL+"/v1").AnalyzeWithSystem(ctx, "system", nil, nil); err == nil || len(calls) != 1 {
		t.Fatalf("api error = %v calls=%d", err, len(calls))
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(empty.Close)
	if _, _, err := newModelForTest(t, empty.URL+"/v1").Analyze(ctx, nil, nil); err == nil {
		t.Fatal("missing choices should fail")
	}
	// A blank system prompt falls back to the default template.
	ok, requests := newChatServer(t, chatReply{content: "done"})
	if _, _, err := newModelForTest(t, ok.URL+"/v1").AnalyzeWithSystem(ctx, " ", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*requests)[0], "资深阅卷老师") {
		t.Fatalf("default system prompt missing: %s", (*requests)[0])
	}
}

func TestPromptCallsAndMessagesSerialization(t *testing.T) {
	if got := AppendPromptCalls("", nil); got != "" {
		t.Fatalf("AppendPromptCalls empty = %q", got)
	}
	first := []PromptCall{{Kind: "analyze", Model: "m", Messages: []PromptMessage{{Role: "user", Content: "a"}}}}
	encoded := AppendPromptCalls("", first)
	if !strings.Contains(encoded, `"analyze"`) {
		t.Fatalf("encoded = %q", encoded)
	}
	second := []PromptCall{{Kind: "continue"}}
	both := AppendPromptCalls(encoded, second)
	if strings.Count(both, `"kind"`) != 2 {
		t.Fatalf("appended = %q", both)
	}
	if got := AppendPromptCalls("not-json", second); strings.Count(got, `"kind"`) != 1 {
		t.Fatalf("invalid existing JSON should reset: %q", got)
	}
	single := promptCall("analyze", "model", 10, []openai.ChatCompletionMessage{{Role: "user", Content: "plain"}})
	if single.Messages[0].Content != "plain" || single.MaxTokens != 10 {
		t.Fatalf("promptCall = %+v", single)
	}

	if EncodeMessages(nil) != "[]" {
		t.Fatal("EncodeMessages empty")
	}
	raw := EncodeMessages([]Message{{Role: "user", Text: "hi"}})
	if decoded := DecodeMessages(raw); len(decoded) != 1 || decoded[0].Text != "hi" {
		t.Fatalf("DecodeMessages = %+v", decoded)
	}
	if DecodeMessages("") != nil || DecodeMessages("not-json") != nil {
		t.Fatal("invalid message JSON should decode to nil")
	}
}

func TestBuildContentPageRange(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "page.webp")
	if err := os.WriteFile(imagePath, []byte("image-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	paper := &Paper{
		TotalPages: 2,
		Pages: []Page{
			{Number: 1, Parts: []Part{{Text: "第一页文本"}, {ImageID: 1}, {ImageID: 9}}},
			{Number: 2, Parts: []Part{{Text: "第二页文本"}}},
		},
		Images: map[int]ImageAsset{1: {ID: 1, Path: imagePath, MIME: "image/webp"}},
	}
	parts, plain, err := BuildContent(paper, 0, 99, "说明")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "说明") || !strings.Contains(plain, "第 1 页") || !strings.Contains(plain, "第二页文本") || !strings.Contains(plain, "[图 1]") {
		t.Fatalf("plain = %q", plain)
	}
	if parts[0].Text != "说明" || len(parts) < 5 {
		t.Fatalf("parts = %+v", parts)
	}
	if _, _, err := BuildContent(paper, 2, 1, "x"); err == nil {
		t.Fatal("reversed range should fail")
	}
	missing := &Paper{TotalPages: 1, Pages: []Page{{Number: 1, Parts: []Part{{ImageID: 3}}}}, Images: map[int]ImageAsset{3: {ID: 3, Path: filepath.Join(dir, "missing.webp")}}}
	_, plain, err = BuildContent(missing, 1, 1, "说明")
	if err != nil || !strings.Contains(plain, "[图 3]") {
		t.Fatalf("missing image plain = %q, %v", plain, err)
	}
	if _, err := imageContentPart(ImageAsset{Path: filepath.Join(dir, "missing.webp")}); err == nil {
		t.Fatal("missing image file should fail")
	}
	part, err := imageContentPart(ImageAsset{Path: imagePath})
	if err != nil || part.ImageURL == nil || !strings.HasPrefix(part.ImageURL.URL, "data:image/webp;base64,") {
		t.Fatalf("image part = %+v, %v", part, err)
	}
}

func TestExtractHelpers(t *testing.T) {
	if got := cleanText("<b>hello</b>&amp;nbsp;\u00a0x"); got != "hello&nbsp; x" {
		t.Fatalf("cleanText = %q", got)
	}
	if zone := verticalZone(1, 100); zone != "页面上部" {
		t.Fatalf("zone top = %q", zone)
	}
	if zone := verticalZone(50, 100); zone != "页面中部" {
		t.Fatalf("zone middle = %q", zone)
	}
	if zone := verticalZone(90, 100); zone != "页面下部" {
		t.Fatalf("zone bottom = %q", zone)
	}
	if zone := verticalZone(10, 0); zone != "页面中" {
		t.Fatalf("zone unknown = %q", zone)
	}
	if got := truncateRunes("短", 5); got != "短" {
		t.Fatalf("truncateRunes short = %q", got)
	}
	if got := truncateRunes("一二三四五六", 3); got != "一二三…" {
		t.Fatalf("truncateRunes long = %q", got)
	}
	if width, height := scaleToMax(4000, 2000, 1600); width != 1600 || height != 800 {
		t.Fatalf("scaleToMax wide = %d,%d", width, height)
	}
	if width, height := scaleToMax(1000, 4000, 1600); width != 400 || height != 1600 {
		t.Fatalf("scaleToMax tall = %d,%d", width, height)
	}
	if maxInt(3, 2) != 3 || maxInt(2, 3) != 3 {
		t.Fatal("maxInt mismatch")
	}
	mimes := map[string]string{".png": "image/png", ".JPG": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".bin": "application/octet-stream", "": "application/octet-stream"}
	for ext, want := range mimes {
		if got := mimeForExt(ext); got != want {
			t.Fatalf("mimeForExt(%q) = %q, want %q", ext, got, want)
		}
	}

	dir := t.TempDir()
	if err := copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "dst")); err == nil {
		t.Fatal("copy of a missing file should fail")
	}
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(dst); string(raw) != "payload" {
		t.Fatalf("copied = %q", raw)
	}
	if err := copyFile(src, filepath.Join(dir, "no-such-dir", "dst")); err == nil {
		t.Fatal("copy into a missing directory should fail")
	}
}

func TestRegisterImageReusesAndSkips(t *testing.T) {
	dir := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x40\x00\x00\x00\x40\x08\x02\x00\x00\x00\x25\x0b\xe6\x89")
	if err := os.WriteFile(filepath.Join(dir, "page-img.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	images := map[int]ImageAsset{}
	seen := map[string]int{}
	next := 1
	opts := ExtractOptions{MaxImages: 10, ImageQuality: 80, ImageMaxEdge: 1600}
	item := &pdfImage{Src: "page-img.png"}
	id, ok := registerImage(context.Background(), item, 1, dir, opts, dir, seen, &next, images)
	if !ok || id != 1 || images[1].Path == "" {
		t.Fatalf("registerImage = %d %v %+v", id, ok, images)
	}
	reused, ok := registerImage(context.Background(), item, 2, dir, opts, dir, seen, &next, images)
	if !ok || reused != 1 || next != 2 {
		t.Fatalf("duplicate = %d %v next=%d", reused, ok, next)
	}
	missing := &pdfImage{Src: "nope.png"}
	if _, ok := registerImage(context.Background(), missing, 1, dir, opts, dir, seen, &next, images); ok {
		t.Fatal("missing image should be skipped")
	}
	next = 11
	if _, ok := registerImage(context.Background(), item, 1, dir, opts, dir, map[string]int{}, &next, images); ok {
		t.Fatal("image limit should skip")
	}
}
