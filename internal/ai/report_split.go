package ai

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	basicHeadingPattern = regexp.MustCompile(`(?mi)^\s*(?:#{1,6}\s*)?【?精简版】?\s*$`)
	moreHeadingPattern  = regexp.MustCompile(`(?mi)^\s*(?:#{1,6}\s*)?【?小学生版】?\s*$`)
	reportMarkerPattern = regexp.MustCompile(`(?i)<!--\s*(?:BASIC|MORE)\s*-->`)
)

// SplitReports separates the single model response into the concise and the
// primary-school reports. The explicit markers are preferred; the Chinese
// section headings are accepted as a fallback when the model forgets them.
func SplitReports(content string) (string, string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")

	basicIndex := indexFold(content, BasicReportMarker)
	moreIndex := indexFold(content, MoreReportMarker)

	var basic, more string
	switch {
	case basicIndex >= 0 && moreIndex > basicIndex:
		basic = content[basicIndex+len(BasicReportMarker) : moreIndex]
		more = content[moreIndex+len(MoreReportMarker):]
	case moreIndex >= 0 && basicIndex < 0:
		basic = content[:moreIndex]
		more = content[moreIndex+len(MoreReportMarker):]
	default:
		basicStart := basicHeadingPattern.FindStringIndex(content)
		moreStart := moreHeadingPattern.FindStringIndex(content)
		switch {
		case basicStart != nil && moreStart != nil && moreStart[0] > basicStart[0]:
			basic = content[basicStart[1]:moreStart[0]]
			more = content[moreStart[1]:]
		case moreStart != nil && basicStart == nil:
			basic = content[:moreStart[0]]
			more = content[moreStart[1]:]
		case basicStart != nil && moreStart == nil:
			basic = content[basicStart[1]:]
		}
	}

	basic = cleanReportPart(basic)
	more = cleanReportPart(more)
	if basic == "" || more == "" {
		return "", "", fmt.Errorf("AI 未按格式输出精简版和小学生版两份解析")
	}
	return basic, more, nil
}

// indexFold is strings.Index with ASCII case folding. The markers are ASCII so
// this is enough and avoids allocating an upper-cased copy for large answers.
func indexFold(text, marker string) int {
	if marker == "" {
		return -1
	}
	limit := len(text) - len(marker)
	for i := 0; i <= limit; i++ {
		match := true
		for j := 0; j < len(marker); j++ {
			a, b := text[i+j], marker[j]
			if 'a' <= a && a <= 'z' {
				a -= 'a' - 'A'
			}
			if 'a' <= b && b <= 'z' {
				b -= 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// cleanReportPart removes leftover markers and a leading section heading that
// the model may have written despite the "only these two sections" rule.
func cleanReportPart(part string) string {
	part = reportMarkerPattern.ReplaceAllString(part, "")
	part = strings.TrimSpace(part)
	for {
		lines := strings.SplitN(part, "\n", 2)
		heading := strings.TrimSpace(lines[0])
		heading = strings.TrimSpace(strings.TrimLeft(heading, "#"))
		heading = strings.TrimSpace(strings.Trim(heading, "【】"))
		if heading != "精简版" && heading != "小学生版" {
			break
		}
		if len(lines) == 1 {
			return ""
		}
		part = strings.TrimSpace(lines[1])
	}
	return part
}
