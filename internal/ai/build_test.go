package ai

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestBuildContentKeepsImagePosition(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "img.webp")
	if err := os.WriteFile(imagePath, []byte("fake-webp"), 0o600); err != nil {
		t.Fatal(err)
	}
	paper := &Paper{
		Title:      "高数",
		TotalPages: 2,
		Pages: []Page{
			{Number: 1, Parts: []Part{
				{Text: "第一段"},
				{Text: "\n【图 1：第 1 页，页面上部】\n"},
				{ImageID: 1},
				{Text: "第二段"},
			}},
			{Number: 2, Parts: []Part{{Text: "第二页"}}},
		},
		Images: map[int]ImageAsset{1: {ID: 1, Path: imagePath, MIME: "image/webp"}},
	}

	parts, plain, err := BuildContent(paper, 1, 2, "解析以下试卷")
	if err != nil {
		t.Fatal(err)
	}
	var types []openai.ChatMessagePartType
	for _, part := range parts {
		types = append(types, part.Type)
	}
	want := []openai.ChatMessagePartType{
		openai.ChatMessagePartTypeText, // instruction
		openai.ChatMessagePartTypeText, // page 1 header
		openai.ChatMessagePartTypeText, // first paragraph
		openai.ChatMessagePartTypeText, // marker
		openai.ChatMessagePartTypeText, // [图 1] label
		openai.ChatMessagePartTypeImageURL,
		openai.ChatMessagePartTypeText, // second paragraph
		openai.ChatMessagePartTypeText, // page 2 header
		openai.ChatMessagePartTypeText,
	}
	if len(types) != len(want) {
		t.Fatalf("parts = %#v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("part %d = %s, want %s", i, types[i], want[i])
		}
	}
	if !strings.Contains(plain, "【图 1") || !strings.Contains(plain, "[图 1]") {
		t.Fatalf("plain text = %q", plain)
	}
}

func TestRenderMarkdownPDF(t *testing.T) {
	if !hasBinary("weasyprint") {
		t.Skip("weasyprint not installed")
	}
	pdf, err := RenderMarkdownPDF(context.Background(), "# 标题\n\n### 第 1 题\n\n**答案：** A\n\n**解析：** 因为 $x=1$。")
	if err != nil {
		t.Fatal(err)
	}
	if len(pdf) < 1000 || string(pdf[:4]) != "%PDF" {
		t.Fatalf("unexpected PDF (%d bytes)", len(pdf))
	}
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
