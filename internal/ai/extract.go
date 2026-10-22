package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"html"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Paper is one extracted exam PDF. Pages hold the text and the inline image
// markers in reading order; Images maps marker ids to encoded files.
type Paper struct {
	Title      string
	TotalPages int
	Pages      []Page
	Images     map[int]ImageAsset
}

// Page is one PDF page. Text is the flattened form used for conversation
// history; Parts keeps the text/image order for multimodal requests.
type Page struct {
	Number int
	Text   string
	Parts  []Part
}

// Part is either a text run or a reference to an extracted image.
type Part struct {
	Text    string
	ImageID int
}

// ImageAsset is an image file ready to be sent to the vision model.
type ImageAsset struct {
	ID     int
	Path   string
	MIME   string
	Page   int
	Width  int
	Height int
}

// ExtractOptions controls PDF extraction and image encoding.
type ExtractOptions struct {
	WorkDir      string
	ImageQuality int
	ImageMaxEdge int
	MaxImages    int
}

const (
	defaultImageQuality = 80
	defaultImageMaxEdge = 1600
	defaultMaxImages    = 120
)

type pdfXML struct {
	Pages []pdfPage `xml:"page"`
}

type pdfPage struct {
	Number int        `xml:"number,attr"`
	Width  float64    `xml:"width,attr"`
	Height float64    `xml:"height,attr"`
	Texts  []pdfText  `xml:"text"`
	Images []pdfImage `xml:"image"`
}

type pdfText struct {
	Top   float64 `xml:"top,attr"`
	Left  float64 `xml:"left,attr"`
	Inner string  `xml:",innerxml"`
}

type pdfImage struct {
	Top    float64 `xml:"top,attr"`
	Left   float64 `xml:"left,attr"`
	Width  float64 `xml:"width,attr"`
	Height float64 `xml:"height,attr"`
	Src    string  `xml:"src,attr"`
}

type pageItem struct {
	top   float64
	left  float64
	text  string
	image *pdfImage
}

var tagPattern = regexp.MustCompile(`<[^>]*>`)

func cleanText(raw string) string {
	text := tagPattern.ReplaceAllString(raw, "")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	return strings.TrimSpace(text)
}

// ExtractPaper runs pdftohtml, converts embedded images to WebP and places an
// 【图 N】 marker at the image's vertical position in the page text.
func ExtractPaper(ctx context.Context, pdfPath, title string, opts ExtractOptions) (*Paper, error) {
	if strings.TrimSpace(opts.WorkDir) == "" {
		return nil, fmt.Errorf("AI work directory is empty")
	}
	if opts.ImageQuality <= 0 {
		opts.ImageQuality = defaultImageQuality
	}
	if opts.ImageMaxEdge <= 0 {
		opts.ImageMaxEdge = defaultImageMaxEdge
	}
	if opts.MaxImages <= 0 {
		opts.MaxImages = defaultMaxImages
	}
	absolutePDF, err := filepath.Abs(pdfPath)
	if err != nil {
		return nil, err
	}
	pdfPath = absolutePDF
	extractDir := filepath.Join(opts.WorkDir, "extract")
	webpDir := filepath.Join(opts.WorkDir, "webp")
	if err := os.MkdirAll(extractDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(webpDir, 0o700); err != nil {
		return nil, err
	}

	doc, err := runPDFToHTML(ctx, pdfPath, extractDir)
	if err != nil {
		return nil, err
	}

	paper := &Paper{Title: title, TotalPages: len(doc.Pages), Images: make(map[int]ImageAsset)}
	seen := make(map[string]bool)
	nextImageID := 1
	for _, page := range doc.Pages {
		items := make([]pageItem, 0, len(page.Texts)+len(page.Images))
		hasText := false
		for _, text := range page.Texts {
			cleaned := cleanText(text.Inner)
			if cleaned == "" {
				continue
			}
			hasText = true
			items = append(items, pageItem{top: text.Top, left: text.Left, text: cleaned})
		}
		for i := range page.Images {
			items = append(items, pageItem{top: page.Images[i].Top, left: page.Images[i].Left, image: &page.Images[i]})
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].top == items[j].top {
				return items[i].left < items[j].left
			}
			return items[i].top < items[j].top
		})

		built := buildPage(ctx, page.Number, page.Height, items, hasText, opts, extractDir, webpDir, seen, &nextImageID, paper.Images)
		paper.Pages = append(paper.Pages, built)
	}
	return paper, nil
}

func runPDFToHTML(ctx context.Context, pdfPath, dir string) (*pdfXML, error) {
	xmlPath := filepath.Join(dir, "pages.xml")
	_ = os.Remove(xmlPath)
	cmd := exec.CommandContext(ctx, "pdftohtml",
		"-xml", "-hidden", "-nomerge", "-fmt", "png", "-zoom", "1.5", pdfPath, "pages.xml")
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// poppler sometimes reports a non-zero status after writing a usable
		// XML file (for example when one page has a damaged font).
		if _, statErr := os.Stat(xmlPath); statErr != nil {
			return nil, fmt.Errorf("pdftohtml failed: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	raw, err := os.ReadFile(xmlPath)
	if err != nil {
		return nil, fmt.Errorf("read pdftohtml output: %w", err)
	}
	var doc pdfXML
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse pdftohtml output: %w", err)
	}
	if len(doc.Pages) == 0 {
		return nil, fmt.Errorf("PDF 没有可读取的页面")
	}
	return &doc, nil
}

func buildPage(ctx context.Context, number int, pageHeight float64, items []pageItem, hasText bool, opts ExtractOptions, extractDir, webpDir string, seen map[string]bool, nextImageID *int, images map[int]ImageAsset) Page {
	var parts []Part
	var pending strings.Builder
	lastTop := -1.0
	lastLine := ""

	flush := func() {
		if pending.Len() == 0 {
			return
		}
		parts = append(parts, Part{Text: pending.String()})
		pending.Reset()
	}
	appendText := func(text string) { pending.WriteString(text) }

	if !hasText {
		// A scanned page has no text layer at all; tell the model explicitly
		// so it knows the following image is the whole page.
		appendText("（本页没有文字层，以下为整页扫描图，请直接识别）\n")
	}

	for _, item := range items {
		if item.image != nil {
			imageID, ok := registerImage(ctx, item.image, number, extractDir, opts, webpDir, seen, nextImageID, images)
			if !ok {
				continue
			}
			zone := verticalZone(item.top, pageHeight)
			marker := fmt.Sprintf("\n【图 %d：第 %d 页，%s", imageID, number, zone)
			if lastLine != "" {
				marker += fmt.Sprintf("，位于“%s”之后】\n", lastLine)
			} else {
				marker += "，位于本页开头】\n"
			}
			appendText(marker)
			flush()
			parts = append(parts, Part{ImageID: imageID})
			lastTop = item.top
			lastLine = fmt.Sprintf("图 %d", imageID)
			continue
		}

		if lastTop >= 0 {
			gap := item.top - lastTop
			switch {
			case gap >= 12:
				appendText("\n\n")
			case gap >= 3:
				appendText("\n")
			case pending.Len() > 0 && !strings.HasSuffix(pending.String(), "\n"):
				appendText(" ")
			}
		}
		appendText(item.text)
		lastTop = item.top
		lastLine = truncateRunes(item.text, 30)
	}
	flush()

	page := Page{Number: number, Parts: parts}
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	page.Text = strings.TrimSpace(text.String())
	if page.Text == "" {
		page.Text = "（本页为空）"
	}
	return page
}

func verticalZone(top, pageHeight float64) string {
	if pageHeight <= 0 {
		return "页面中"
	}
	ratio := top / pageHeight
	switch {
	case ratio < 0.34:
		return "页面上部"
	case ratio < 0.67:
		return "页面中部"
	default:
		return "页面下部"
	}
}

func truncateRunes(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "…"
}

func registerImage(ctx context.Context, item *pdfImage, page int, extractDir string, opts ExtractOptions, webpDir string, seen map[string]bool, nextImageID *int, images map[int]ImageAsset) (int, bool) {
	if *nextImageID > opts.MaxImages {
		return 0, false
	}
	src := filepath.Join(extractDir, filepath.Base(item.Src))
	raw, err := os.ReadFile(src)
	if err != nil {
		return 0, false
	}
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])
	if seen[key] {
		return 0, false
	}
	seen[key] = true

	width, height := 0, 0
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
		width, height = cfg.Width, cfg.Height
	}
	if width > 0 && height > 0 && (width < 32 || height < 32 || width*height < 1500) {
		return 0, false
	}

	id := *nextImageID
	dst := filepath.Join(webpDir, fmt.Sprintf("img-%03d.webp", id))
	mime, err := encodeWebP(ctx, src, dst, opts.ImageQuality, opts.ImageMaxEdge, width, height)
	if err != nil {
		dst = filepath.Join(webpDir, fmt.Sprintf("img-%03d%s", id, filepath.Ext(src)))
		if copyErr := copyFile(src, dst); copyErr != nil {
			return 0, false
		}
		mime = mimeForExt(filepath.Ext(src))
	}
	*nextImageID++
	images[id] = ImageAsset{ID: id, Path: dst, MIME: mime, Page: page, Width: width, Height: height}
	return id, true
}

func encodeWebP(ctx context.Context, src, dst string, quality, maxEdge, width, height int) (string, error) {
	args := []string{"-quiet", "-q", strconv.Itoa(quality), "-metadata", "none"}
	if width > 0 && height > 0 && (width > maxEdge || height > maxEdge) {
		targetW, targetH := scaleToMax(width, height, maxEdge)
		args = append(args, "-resize", strconv.Itoa(targetW), strconv.Itoa(targetH))
	}
	args = append(args, src, "-o", dst)
	cmd := exec.CommandContext(ctx, "cwebp", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cwebp failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if info, err := os.Stat(dst); err != nil || info.Size() == 0 {
		return "", fmt.Errorf("cwebp produced an empty file")
	}
	return "image/webp", nil
}

func scaleToMax(width, height, maxEdge int) (int, int) {
	if width >= height {
		return maxEdge, maxInt(1, height*maxEdge/width)
	}
	return maxInt(1, width*maxEdge/height), maxEdge
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func mimeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}
