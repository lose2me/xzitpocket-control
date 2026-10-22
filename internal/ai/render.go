package ai

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

const pdfCSS = `
@page {
  size: A4;
  margin: 18mm 16mm 16mm 16mm;
  @bottom-center {
    content: counter(page);
    font-size: 9pt;
    color: #666;
  }
}
body {
  font-family: "Noto Sans CJK SC", "Noto Sans SC", "Source Han Sans SC", sans-serif;
  font-size: 10.5pt;
  line-height: 1.65;
  color: #111;
}
h1 { font-size: 17pt; margin: 0 0 6mm; }
h2 { font-size: 13.5pt; margin: 6mm 0 2mm; }
h3 { font-size: 11.5pt; margin: 4.5mm 0 1.5mm; page-break-after: avoid; }
p { margin: 1.2mm 0; }
ul, ol { margin: 1.2mm 0 1.2mm 5mm; padding: 0; }
li { margin: 0.6mm 0; }
strong { font-weight: 600; }
code {
  font-family: "DejaVu Sans Mono", monospace;
  font-size: 9.5pt;
  background: #f2f2f2;
  padding: 0 1mm;
}
pre { background: #f5f5f5; padding: 2mm; white-space: pre-wrap; }
table { border-collapse: collapse; margin: 2mm 0; width: 100%; }
th, td { border: 0.3mm solid #999; padding: 1mm 1.5mm; font-size: 10pt; }
th { background: #f0f0f0; }
blockquote { margin: 2mm 0; padding-left: 3mm; border-left: 0.8mm solid #ccc; color: #444; }
hr { border: 0; border-top: 0.3mm solid #ccc; margin: 4mm 0; }
`

// RenderMarkdownPDF converts the analysis markdown into a PDF with WeasyPrint.
func RenderMarkdownPDF(ctx context.Context, markdown string) ([]byte, error) {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return nil, fmt.Errorf("解析结果为空")
	}
	var body bytes.Buffer
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := parser.Convert([]byte(markdown), &body); err != nil {
		return nil, fmt.Errorf("markdown 转换失败: %w", err)
	}
	var html bytes.Buffer
	html.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><style>")
	html.WriteString(pdfCSS)
	html.WriteString("</style></head><body>")
	html.Write(body.Bytes())
	html.WriteString("</body></html>")

	cmd := exec.CommandContext(ctx, "weasyprint", "-", "-")
	cmd.Stdin = bytes.NewReader(html.Bytes())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("weasyprint 生成 PDF 失败: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("weasyprint 未生成 PDF")
	}
	return stdout.Bytes(), nil
}
