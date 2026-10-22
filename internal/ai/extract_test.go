package ai

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractPaperTextAndImages exercises the real poppler + cwebp pipeline on
// a PDF generated at test time, so no binary fixture lives in the repository.
func TestExtractPaperTextAndImages(t *testing.T) {
	for _, binary := range []string{"pdftohtml", "cwebp", "weasyprint"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s not installed", binary)
		}
	}
	pdfPath := writeSamplePDF(t)
	paper, err := ExtractPaper(context.Background(), pdfPath, "高等数学期末试卷", ExtractOptions{
		WorkDir:      t.TempDir(),
		ImageQuality: 80,
		ImageMaxEdge: 1600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if paper.TotalPages != 1 {
		t.Fatalf("total pages = %d", paper.TotalPages)
	}
	if !strings.Contains(paper.Pages[0].Text, "高等数学") {
		t.Fatalf("page text = %q", paper.Pages[0].Text)
	}
	if !strings.Contains(paper.Pages[0].Text, "【图 1") {
		t.Fatalf("image marker missing: %q", paper.Pages[0].Text)
	}
	asset, ok := paper.Images[1]
	if !ok || asset.MIME != "image/webp" {
		t.Fatalf("image asset = %#v", paper.Images)
	}
}

// writeSamplePDF builds a one-page Chinese exam PDF with a figure.
func writeSamplePDF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 240, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 240; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y * 2), B: 180, A: 255})
		}
	}
	imageFile, err := os.Create(filepath.Join(dir, "fig.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(imageFile, img); err != nil {
		t.Fatal(err)
	}
	imageFile.Close()
	html := `<html><head><meta charset="utf-8"><style>
@page { size: A4; margin: 18mm; }
body { font-family: "Noto Sans CJK SC"; font-size: 11pt; line-height: 1.7; }
</style></head><body>
<h1>高等数学 期末考试试卷</h1>
<p>1. 设函数 f(x)=x^2+2x+1，则 f'(x) 等于（ ）</p>
<p>A. 2x+1　B. 2x+2　C. x+2　D. 2x</p>
<p><img src="fig.png" style="width:60mm"></p>
<p>2. 计算定积分 ∫_0^1 x^2 dx 的值。</p>
</body></html>`
	htmlPath := filepath.Join(dir, "sample.html")
	pdfPath := filepath.Join(dir, "sample.pdf")
	if err := os.WriteFile(htmlPath, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("weasyprint", "sample.html", "sample.pdf")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("weasyprint: %v: %s", err, out)
	}
	return pdfPath
}
