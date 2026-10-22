package ai

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Window is a daily half-open interval [Start, End) in minutes after midnight.
// End may be 1440 to mean midnight at the end of the day.
type Window struct {
	Start int
	End   int
}

// ParseWindows parses "00:00-08:50,12:00-13:50,18:00-24:00".
func ParseWindows(raw string) ([]Window, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("AI time windows are empty")
	}
	pieces := strings.Split(raw, ",")
	windows := make([]Window, 0, len(pieces))
	for _, piece := range pieces {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		parts := strings.SplitN(piece, "-", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid AI time window %q", piece)
		}
		start, err := parseClock(parts[0], false)
		if err != nil {
			return nil, fmt.Errorf("invalid AI time window %q: %w", piece, err)
		}
		end, err := parseClock(parts[1], true)
		if err != nil {
			return nil, fmt.Errorf("invalid AI time window %q: %w", piece, err)
		}
		if end <= start {
			return nil, fmt.Errorf("invalid AI time window %q: end must be after start", piece)
		}
		windows = append(windows, Window{Start: start, End: end})
	}
	if len(windows) == 0 {
		return nil, fmt.Errorf("AI time windows are empty")
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].Start < windows[j].Start })
	for i := 1; i < len(windows); i++ {
		if windows[i].Start < windows[i-1].End {
			return nil, fmt.Errorf("AI time windows overlap at %d", windows[i].Start)
		}
	}
	return windows, nil
}

func parseClock(raw string, allowEndOfDay bool) (int, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("want HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 24 {
		return 0, fmt.Errorf("invalid hour %q", parts[0])
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("invalid minute %q", parts[1])
	}
	total := hour*60 + minute
	if total == 1440 && !allowEndOfDay {
		return 0, fmt.Errorf("24:00 is only valid as an end")
	}
	if total > 1440 {
		return 0, fmt.Errorf("time out of range")
	}
	return total, nil
}

// InWindow reports whether t (already in the configured location) is inside
// one of the daily windows.
func InWindow(windows []Window, t time.Time) bool {
	minute := t.Hour()*60 + t.Minute()
	for _, window := range windows {
		if minute >= window.Start && minute < window.End {
			return true
		}
	}
	return false
}

// WindowEnd returns the end of the window containing t. It walks into the
// next day when the window ends at 24:00.
func WindowEnd(windows []Window, t time.Time) (time.Time, bool) {
	minute := t.Hour()*60 + t.Minute()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	for _, window := range windows {
		if minute < window.Start || minute >= window.End {
			continue
		}
		if window.End == 1440 {
			return day.AddDate(0, 0, 1), true
		}
		return day.Add(time.Duration(window.End) * time.Minute), true
	}
	return time.Time{}, false
}

// NextWindowStart returns the next window start after t. When today has no
// window left, the first window of tomorrow is returned.
func NextWindowStart(windows []Window, t time.Time) time.Time {
	minute := t.Hour()*60 + t.Minute()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	for _, window := range windows {
		if minute < window.Start {
			return day.Add(time.Duration(window.Start) * time.Minute)
		}
	}
	return day.AddDate(0, 0, 1).Add(time.Duration(windows[0].Start) * time.Minute)
}
