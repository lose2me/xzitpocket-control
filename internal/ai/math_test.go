package ai

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractMath(t *testing.T) {
	markdown := "题目 $x^2+2x+1$ 后面\n\n$$\\frac{1}{3}$$\n\n\\[\\int_0^1 x^2\\,dx\\]\n\n代码 `$not$` 和 \\$5 不是公式\n\n```\n$code$\n```\n"
	extracted, exprs := extractMath(markdown)
	if len(exprs) != 3 {
		t.Fatalf("exprs = %#v", exprs)
	}
	if exprs[0].Tex != "x^2+2x+1" || exprs[0].Display {
		t.Fatalf("inline = %#v", exprs[0])
	}
	if exprs[1].Tex != `\frac{1}{3}` || !exprs[1].Display {
		t.Fatalf("display = %#v", exprs[1])
	}
	if exprs[2].Tex != `\int_0^1 x^2\,dx` || !exprs[2].Display {
		t.Fatalf("bracket display = %#v", exprs[2])
	}
	if !strings.Contains(extracted, "@@MATH_I_0@@") || !strings.Contains(extracted, "@@MATH_D_1@@") {
		t.Fatalf("placeholders missing: %q", extracted)
	}
	if !strings.Contains(extracted, "`$not$`") || !strings.Contains(extracted, "$5") {
		t.Fatalf("code/currency changed: %q", extracted)
	}
	if !strings.Contains(extracted, "```\n$code$\n```") {
		t.Fatalf("fenced code changed: %q", extracted)
	}
}

func TestExtractMathUnclosedDollar(t *testing.T) {
	extracted, exprs := extractMath("价格 $5 and $10 元")
	if len(exprs) != 0 {
		t.Fatalf("exprs = %#v", exprs)
	}
	if extracted != "价格 $5 and $10 元" {
		t.Fatalf("extracted = %q", extracted)
	}
}

func TestInjectMath(t *testing.T) {
	html := []byte("<p>前 @@MATH_I_0@@ 后</p>\n<p>@@MATH_D_1@@</p>")
	out := string(injectMath(html, []string{"<svg>inline</svg>", "<svg>display</svg>"}))
	if !strings.Contains(out, `<span class="math-inline"><svg>inline</svg></span>`) {
		t.Fatalf("inline not injected: %s", out)
	}
	if !strings.Contains(out, `<div class="math-display"><svg>display</svg></div>`) {
		t.Fatalf("display not injected: %s", out)
	}
}

func TestRenderMathSVGs(t *testing.T) {
	dir := filepath.Join("..", "..", "ai-math")
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "mathjax-full")); err != nil {
		t.Skip("mathjax not installed")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	svgs, err := renderMathSVGs(context.Background(), dir, []mathExpr{
		{Tex: `\frac{1}{3}`},
		{Tex: `\begin{bmatrix}a&b\\c&d\end{bmatrix}`, Display: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(svgs) != 2 || !strings.Contains(svgs[0], "<svg") || strings.Contains(svgs[0], "mjx-merror") {
		t.Fatalf("svgs = %#v", svgs)
	}
}

func TestRenderMarkdownPDFWithMath(t *testing.T) {
	dir := filepath.Join("..", "..", "ai-math")
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "mathjax-full")); err != nil {
		t.Skip("mathjax not installed")
	}
	for _, binary := range []string{"node", "weasyprint"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s not installed", binary)
		}
	}
	pdf, err := RenderMarkdownPDF(context.Background(), "### 第 1 题\n\n**答案：** $\\frac{1}{3}$\n\n$$A^{-1}=\\frac{1}{ad-bc}\\begin{bmatrix}d&-b\\\\-c&a\\end{bmatrix}$$", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("not a PDF (%d bytes)", len(pdf))
	}
	// MathJax SVG is drawn as paths, so the raw LaTeX must be gone.
	if _, err := exec.LookPath("pdftotext"); err == nil {
		cmd := exec.Command("pdftotext", "-", "-")
		cmd.Stdin = bytes.NewReader(pdf)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{"frac", "bmatrix", "A^{-1}"} {
			if strings.Contains(string(out), raw) {
				t.Fatalf("raw LaTeX %q still in PDF text: %s", raw, out)
			}
		}
	}
}
