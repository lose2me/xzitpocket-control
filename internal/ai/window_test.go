package ai

import (
	"testing"
	"time"
)

func TestParseWindows(t *testing.T) {
	windows, err := ParseWindows("00:00-08:50,12:00-13:50,18:00-24:00")
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 3 {
		t.Fatalf("windows = %#v", windows)
	}
	if windows[2].End != 1440 {
		t.Fatalf("last end = %d", windows[2].End)
	}
	for _, raw := range []string{"", "8-9", "18:00-24:00,23:00-24:00", "25:00-26:00", "24:00-24:30"} {
		if _, err := ParseWindows(raw); err == nil {
			t.Fatalf("ParseWindows(%q) should fail", raw)
		}
	}
}

func TestWindowBoundaries(t *testing.T) {
	windows, _ := ParseWindows("00:00-08:50,12:00-13:50,18:00-24:00")
	location := time.FixedZone("CST", 8*3600)
	at := func(hour, minute int) time.Time {
		return time.Date(2026, 3, 2, hour, minute, 0, 0, location)
	}
	if !InWindow(windows, at(0, 0)) || InWindow(windows, at(8, 50)) || InWindow(windows, at(13, 50)) {
		t.Fatal("window boundaries are wrong")
	}
	if !InWindow(windows, at(23, 59)) {
		t.Fatal("18:00-24:00 should include 23:59")
	}
	end, ok := WindowEnd(windows, at(23, 30))
	if !ok || !end.Equal(at(0, 0).AddDate(0, 0, 1)) {
		t.Fatalf("window end = %v, ok = %v", end, ok)
	}
	if _, ok := WindowEnd(windows, at(9, 0)); ok {
		t.Fatal("09:00 should be outside every window")
	}
	next := NextWindowStart(windows, at(9, 0))
	if !next.Equal(at(12, 0)) {
		t.Fatalf("next start = %v", next)
	}
}

func TestParseVerdict(t *testing.T) {
	verdict, err := parseVerdict("```json\n{\"solvable\":true,\"reason\":\"完整试卷\",\"subject\":\"数学\",\"question_count\":12}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Solvable || verdict.QuestionCount != 12 || verdict.Subject != "数学" {
		t.Fatalf("verdict = %#v", verdict)
	}
	if _, err := parseVerdict("无法判断"); err == nil {
		t.Fatal("non-JSON should fail")
	}
}
