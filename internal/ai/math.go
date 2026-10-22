package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// mathExpr is one LaTeX expression extracted from the analysis markdown.
type mathExpr struct {
	Tex     string `json:"tex"`
	Display bool   `json:"display"`
}

var (
	displayPlaceholderRE = regexp.MustCompile(`(?s)<p>\s*@@MATH_D_(\d+)@@\s*</p>`)
	orphanDisplayRE      = regexp.MustCompile(`@@MATH_D_(\d+)@@`)
	inlinePlaceholderRE  = regexp.MustCompile(`@@MATH_I_(\d+)@@`)
)

// extractMath replaces inline and display LaTeX with placeholders. Fenced code
// blocks, code spans and escaped dollar signs are left untouched.
func extractMath(markdown string) (string, []mathExpr) {
	var out strings.Builder
	exprs := make([]mathExpr, 0, 8)
	var chunk strings.Builder
	flush := func() {
		if chunk.Len() == 0 {
			return
		}
		processMathChunk(chunk.String(), &out, &exprs)
		chunk.Reset()
	}
	inFence := false
	fence := ""
	for _, line := range strings.SplitAfter(markdown, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			flush()
			inFence = true
			fence = trimmed[:3]
			out.WriteString(line)
			continue
		}
		if inFence {
			out.WriteString(line)
			if strings.HasPrefix(trimmed, fence) {
				inFence = false
			}
			continue
		}
		chunk.WriteString(line)
	}
	flush()
	return out.String(), exprs
}

func processMathChunk(s string, out *strings.Builder, exprs *[]mathExpr) {
	add := func(tex string, display bool) {
		tex = strings.TrimSpace(tex)
		if tex == "" {
			return
		}
		id := len(*exprs)
		*exprs = append(*exprs, mathExpr{Tex: tex, Display: display})
		if display {
			out.WriteString("\n\n@@MATH_D_" + strconv.Itoa(id) + "@@\n\n")
		} else {
			out.WriteString("@@MATH_I_" + strconv.Itoa(id) + "@@")
		}
	}

	for i := 0; i < len(s); {
		// Inline code spans are copied verbatim.
		if s[i] == '`' {
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				end = i + 1 + end + 1
				out.WriteString(s[i:end])
				i = end
				continue
			}
			out.WriteByte(s[i])
			i++
			continue
		}
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '[':
				if end := strings.Index(s[i+2:], `\]`); end >= 0 {
					add(s[i+2:i+2+end], true)
					i = i + 2 + end + 2
					continue
				}
			case '(':
				if end := strings.Index(s[i+2:], `\)`); end >= 0 {
					add(s[i+2:i+2+end], false)
					i = i + 2 + end + 2
					continue
				}
			case '$':
				out.WriteString(`\$`)
				i += 2
				continue
			}
			out.WriteByte(s[i])
			i++
			continue
		}
		if s[i] == '$' {
			if i+1 < len(s) && s[i+1] == '$' {
				if end := strings.Index(s[i+2:], "$$"); end >= 0 {
					add(s[i+2:i+2+end], true)
					i = i + 2 + end + 2
					continue
				}
			}
			// Inline math must end on the same line and must not start or
			// end with whitespace (this keeps "$5 and $10" as plain text).
			if i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '\t' && s[i+1] != '\n' {
				j := i + 1
				for j < len(s) && s[j] != '\n' {
					if s[j] == '\\' {
						j += 2
						continue
					}
					if s[j] == '$' {
						break
					}
					j++
				}
				if j < len(s) && s[j] == '$' && j > i+1 && s[j-1] != ' ' && s[j-1] != '\t' {
					add(s[i+1:j], false)
					i = j + 1
					continue
				}
			}
			out.WriteByte('$')
			i++
			continue
		}
		out.WriteByte(s[i])
		i++
	}
}

// renderMathSVGs asks the Node/MathJax helper for one SVG per expression.
func renderMathSVGs(ctx context.Context, dir string, exprs []mathExpr) ([]string, error) {
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dir = absoluteDir
	script := filepath.Join(dir, "render.js")
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("math renderer not installed: %w", err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("node not found: %w", err)
	}
	payload, err := json.Marshal(exprs)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, node, script)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("math renderer failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var svgs []string
	if err := json.Unmarshal(stdout.Bytes(), &svgs); err != nil {
		return nil, fmt.Errorf("parse math renderer output: %w", err)
	}
	if len(svgs) != len(exprs) {
		return nil, fmt.Errorf("math renderer returned %d of %d formulas", len(svgs), len(exprs))
	}
	for _, svg := range svgs {
		if strings.TrimSpace(svg) == "" {
			return nil, fmt.Errorf("math renderer returned an empty formula")
		}
	}
	return svgs, nil
}

// injectMath swaps placeholders in the goldmark output for the SVG fragments.
func injectMath(html []byte, svgs []string) []byte {
	result := displayPlaceholderRE.ReplaceAllStringFunc(string(html), func(match string) string {
		index := placeholderIndex(match)
		if index < 0 || index >= len(svgs) {
			return match
		}
		return `<div class="math-display">` + svgs[index] + `</div>`
	})
	result = orphanDisplayRE.ReplaceAllStringFunc(result, func(match string) string {
		index := placeholderIndex(match)
		if index < 0 || index >= len(svgs) {
			return match
		}
		return `<div class="math-display">` + svgs[index] + `</div>`
	})
	result = inlinePlaceholderRE.ReplaceAllStringFunc(result, func(match string) string {
		index := placeholderIndex(match)
		if index < 0 || index >= len(svgs) {
			return match
		}
		return `<span class="math-inline">` + svgs[index] + `</span>`
	})
	return []byte(result)
}

func placeholderIndex(match string) int {
	start := strings.LastIndexByte(match, '_')
	if start < 0 {
		return -1
	}
	end := strings.IndexByte(match[start:], '@')
	if end < 0 {
		return -1
	}
	value, err := strconv.Atoi(match[start+1 : start+end])
	if err != nil {
		return -1
	}
	return value
}
