package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

var schoolCalendarDatePattern = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
var schoolCalendarAdjustmentPattern = regexp.MustCompile(`^\d{8}$`)
var schoolCalendarClockPattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

type SchoolCalendarDay struct {
	Date       string `json:"date"`
	Name       string `json:"name"`
	Adjustment string `json:"adjustment"`
}

type SchoolCalendarInput struct {
	Days  []SchoolCalendarDay  `json:"days"`
	Slots []SchoolCalendarSlot `json:"slots"`
}

// SchoolCalendarSlot is one timetable period (作息时间). Index is 1-based and
// Start/End are zero-padded 24h "HH:MM" strings.
type SchoolCalendarSlot struct {
	Index int    `json:"index"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type SchoolCalendarView struct {
	Days      []SchoolCalendarDay  `json:"days"`
	Slots     []SchoolCalendarSlot `json:"slots"`
	UpdatedAt string               `json:"updatedAt"`
}

func validateSchoolCalendarInput(in SchoolCalendarInput) (string, []SchoolCalendarDay, error) {
	if len(in.Days) == 0 || len(in.Days) > 1000 {
		return "", nil, Err("invalid_school_calendar", "校历日期数量无效", http.StatusBadRequest)
	}
	seen := make(map[string]bool, len(in.Days))
	for i := range in.Days {
		day := &in.Days[i]
		day.Date = strings.TrimSpace(day.Date)
		_, normalizedDate, err := parseSchoolCalendarDate(day.Date)
		if err != nil {
			return "", nil, Err("invalid_school_calendar", "校历日期格式无效", http.StatusBadRequest)
		}
		day.Date = normalizedDate
		if seen[day.Date] {
			return "", nil, Err("invalid_school_calendar", "校历日期不能重复", http.StatusBadRequest)
		}
		seen[day.Date] = true
		day.Name = strings.TrimSpace(day.Name)
		if len(day.Name) > 64 || strings.ContainsAny(day.Name, "\r\n\x00") {
			return "", nil, Err("invalid_school_calendar", "校历名称无效", http.StatusBadRequest)
		}
		day.Adjustment = strings.TrimSpace(day.Adjustment)
		if day.Adjustment != "" && day.Adjustment != "/" {
			if !schoolCalendarAdjustmentPattern.MatchString(day.Adjustment) {
				return "", nil, Err("invalid_school_calendar", "课程调整日期格式无效", http.StatusBadRequest)
			}
			if _, err := time.Parse("20060102", day.Adjustment); err != nil {
				return "", nil, Err("invalid_school_calendar", "课程调整日期无效", http.StatusBadRequest)
			}
		}
	}
	sort.Slice(in.Days, func(i, j int) bool { return in.Days[i].Date < in.Days[j].Date })
	for i := 1; i < len(in.Days); i++ {
		previous, _ := time.Parse("2006-01-02", in.Days[i-1].Date)
		current, _ := time.Parse("2006-01-02", in.Days[i].Date)
		if current.Sub(previous) != 24*time.Hour {
			return "", nil, Err("invalid_school_calendar", "校历日期必须连续且不能重复", http.StatusBadRequest)
		}
	}
	encoded, err := json.Marshal(in.Days)
	if err != nil {
		return "", nil, err
	}
	return string(encoded), in.Days, nil
}

func defaultSchoolCalendarSlots() []SchoolCalendarSlot {
	return []SchoolCalendarSlot{
		{Index: 1, Start: "08:00", End: "08:45"},
		{Index: 2, Start: "08:55", End: "09:40"},
		{Index: 3, Start: "10:05", End: "10:50"},
		{Index: 4, Start: "11:00", End: "11:45"},
		{Index: 5, Start: "12:00", End: "12:45"},
		{Index: 6, Start: "12:55", End: "13:40"},
		{Index: 7, Start: "14:00", End: "14:45"},
		{Index: 8, Start: "14:55", End: "15:40"},
		{Index: 9, Start: "16:05", End: "16:50"},
		{Index: 10, Start: "17:00", End: "17:45"},
		{Index: 11, Start: "17:55", End: "18:40"},
		{Index: 12, Start: "18:45", End: "19:30"},
		{Index: 13, Start: "19:40", End: "20:25"},
		{Index: 14, Start: "20:35", End: "21:20"},
	}
}

// validateSchoolCalendarSlots normalizes and validates the timetable periods.
// An empty list falls back to the built-in 14-period schedule. Indices are
// renumbered 1..N so clients can rely on the order.
func validateSchoolCalendarSlots(in []SchoolCalendarSlot) (string, []SchoolCalendarSlot, error) {
	if len(in) == 0 {
		in = defaultSchoolCalendarSlots()
	}
	if len(in) > 30 {
		return "", nil, Err("invalid_school_calendar", "节次数量无效", http.StatusBadRequest)
	}
	out := make([]SchoolCalendarSlot, len(in))
	for i := range in {
		slot := SchoolCalendarSlot{
			Index: i + 1,
			Start: strings.TrimSpace(in[i].Start),
			End:   strings.TrimSpace(in[i].End),
		}
		if !schoolCalendarClockPattern.MatchString(slot.Start) || !schoolCalendarClockPattern.MatchString(slot.End) {
			return "", nil, Err("invalid_school_calendar", "节次时间格式无效，应为 HH:MM", http.StatusBadRequest)
		}
		if slot.Start >= slot.End {
			return "", nil, Err("invalid_school_calendar", "节次结束时间必须晚于开始时间", http.StatusBadRequest)
		}
		out[i] = slot
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", nil, err
	}
	return string(encoded), out, nil
}

func parseSchoolCalendarDate(value string) (time.Time, string, error) {
	match := schoolCalendarDatePattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return time.Time{}, "", errors.New("invalid date")
	}
	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	parsed := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if parsed.Year() != year || int(parsed.Month()) != month || parsed.Day() != day {
		return time.Time{}, "", errors.New("invalid date")
	}
	return parsed, parsed.Format("2006-01-02"), nil
}

func schoolCalendarView(config sqlite.SchoolCalendarConfig, days []SchoolCalendarDay, slots []SchoolCalendarSlot) SchoolCalendarView {
	out := SchoolCalendarView{Days: days, Slots: slots}
	out.UpdatedAt = configVersion(config.UpdatedAt)
	return out
}

func decodeStoredSchoolCalendar(raw string) ([]SchoolCalendarDay, error) {
	var days []SchoolCalendarDay
	if err := json.Unmarshal([]byte(raw), &days); err != nil {
		return nil, err
	}
	return days, nil
}

func decodeStoredSchoolCalendarSlots(raw string) ([]SchoolCalendarSlot, error) {
	var slots []SchoolCalendarSlot
	if err := json.Unmarshal([]byte(raw), &slots); err != nil {
		return nil, err
	}
	return slots, nil
}

func defaultSchoolCalendarDays() []SchoolCalendarDay {
	start := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, time.January, 10, 0, 0, 0, 0, time.UTC)
	festivals := map[string]string{
		"2026-09-25": "中秋",
		"2026-10-01": "国庆",
		"2027-01-01": "元旦",
	}
	days := make([]SchoolCalendarDay, 0, int(end.Sub(start)/(24*time.Hour))+1)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		days = append(days, SchoolCalendarDay{
			Date:       key,
			Name:       festivals[key],
			Adjustment: "",
		})
	}
	return days
}

func (a *App) GetSchoolCalendar(ctx context.Context) (SchoolCalendarView, error) {
	config, err := a.Store.GetSchoolCalendarConfig(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return SchoolCalendarView{}, ErrNotFound
	}
	if err != nil {
		return SchoolCalendarView{}, err
	}
	days, err := decodeStoredSchoolCalendar(config.DaysJSON)
	if err != nil || len(days) == 0 {
		return a.resetSchoolCalendar(ctx)
	}
	_, normalized, validationErr := validateSchoolCalendarInput(SchoolCalendarInput{Days: days})
	if validationErr != nil {
		return a.resetSchoolCalendar(ctx)
	}
	days = normalized
	slots, slotErr := decodeStoredSchoolCalendarSlots(config.SlotsJSON)
	if slotErr != nil || len(slots) == 0 {
		slots = defaultSchoolCalendarSlots()
	} else if _, normalizedSlots, err := validateSchoolCalendarSlots(slots); err != nil {
		slots = defaultSchoolCalendarSlots()
	} else {
		slots = normalizedSlots
	}
	return schoolCalendarView(config, days, slots), nil
}

func (a *App) resetSchoolCalendar(ctx context.Context) (SchoolCalendarView, error) {
	encoded, days, err := validateSchoolCalendarInput(SchoolCalendarInput{Days: defaultSchoolCalendarDays()})
	if err != nil {
		return SchoolCalendarView{}, err
	}
	encodedSlots, slots, err := validateSchoolCalendarSlots(nil)
	if err != nil {
		return SchoolCalendarView{}, err
	}
	now := time.Now().UTC()
	config, err := a.Store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{DaysJSON: encoded, SlotsJSON: encodedSlots, UpdatedAt: now})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SchoolCalendarView{}, ErrNotFound
		}
		return SchoolCalendarView{}, err
	}
	return schoolCalendarView(config, days, slots), nil
}

func (a *App) UpdateSchoolCalendar(ctx context.Context, in SchoolCalendarInput, actor string) (SchoolCalendarView, error) {
	encoded, days, err := validateSchoolCalendarInput(in)
	if err != nil {
		return SchoolCalendarView{}, err
	}
	encodedSlots, slots, err := validateSchoolCalendarSlots(in.Slots)
	if err != nil {
		return SchoolCalendarView{}, err
	}
	now := time.Now().UTC()
	config, err := a.Store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{DaysJSON: encoded, SlotsJSON: encodedSlots, UpdatedAt: now})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SchoolCalendarView{}, ErrNotFound
		}
		return SchoolCalendarView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "school_calendar_update", "school_calendar", "current",
		controlcrypto.JSON(map[string]any{"days": len(days), "slots": len(slots)}), now)
	return schoolCalendarView(config, days, slots), nil
}
