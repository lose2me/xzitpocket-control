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
	// Lab marks a lab period (实验时段): the client shows a "T" next to it and
	// hides it by default when no course falls on it.
	Lab bool `json:"lab"`
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
		{Index: 5, Start: "12:00", End: "12:45", Lab: true},
		{Index: 6, Start: "12:55", End: "13:40", Lab: true},
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
			Lab:   in[i].Lab,
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

// SchoolCalendarCollegeView is one college's exception list in the console.
type SchoolCalendarCollegeView struct {
	College    string              `json:"college"`
	Exceptions []SchoolCalendarDay `json:"exceptions"`
	UpdatedAt  string              `json:"updatedAt"`
}

// SchoolCalendarAdminView adds the per-college overrides to the global view.
type SchoolCalendarAdminView struct {
	SchoolCalendarView
	Colleges       []SchoolCalendarCollegeView `json:"colleges"`
	CollegeOptions []string                    `json:"collegeOptions"`
}

// SchoolCalendarExceptionsInput is the console payload for one college (or the
// global calendar when College is empty).
type SchoolCalendarExceptionsInput struct {
	College    string              `json:"college"`
	Exceptions []SchoolCalendarDay `json:"exceptions"`
}

// calendarExceptions keeps only the days that carry a name or an adjustment.
func calendarExceptions(days []SchoolCalendarDay) []SchoolCalendarDay {
	out := make([]SchoolCalendarDay, 0, len(days))
	for _, day := range days {
		if day.Name != "" || day.Adjustment != "" {
			out = append(out, day)
		}
	}
	return out
}

// GetSchoolCalendarForCollege returns the calendar for one college: its own
// exceptions when configured, otherwise the global calendar.
func (a *App) GetSchoolCalendarForCollege(ctx context.Context, college string) (SchoolCalendarView, error) {
	base, err := a.GetSchoolCalendar(ctx)
	if err != nil {
		return SchoolCalendarView{}, err
	}
	college = strings.TrimSpace(college)
	if college == "" {
		return base, nil
	}
	override, ok, err := a.Store.GetSchoolCalendarCollege(ctx, college)
	if err != nil || !ok {
		return base, nil
	}
	days, decodeErr := decodeStoredSchoolCalendar(override.DaysJSON)
	if decodeErr != nil || len(days) == 0 {
		return base, nil
	}
	base.Days = days
	return base, nil
}

// GetSchoolCalendarAdmin returns the global calendar plus every college
// override and the college names the console can pick from.
func (a *App) GetSchoolCalendarAdmin(ctx context.Context) (SchoolCalendarAdminView, error) {
	base, err := a.GetSchoolCalendar(ctx)
	if err != nil {
		return SchoolCalendarAdminView{}, err
	}
	overrides, err := a.Store.ListSchoolCalendarColleges(ctx)
	if err != nil {
		return SchoolCalendarAdminView{}, err
	}
	views := make([]SchoolCalendarCollegeView, 0, len(overrides))
	for _, item := range overrides {
		days, decodeErr := decodeStoredSchoolCalendar(item.DaysJSON)
		if decodeErr != nil {
			continue
		}
		views = append(views, SchoolCalendarCollegeView{
			College: item.College, Exceptions: calendarExceptions(days), UpdatedAt: configVersion(item.UpdatedAt),
		})
	}
	options, err := a.Store.ListUserColleges(ctx)
	if err != nil {
		return SchoolCalendarAdminView{}, err
	}
	return SchoolCalendarAdminView{
		SchoolCalendarView: base, Colleges: views, CollegeOptions: options,
	}, nil
}

// SaveSchoolCalendarExceptions stores the exception list of one college, or of
// the global calendar when College is empty. The full day range is rebuilt from
// the global calendar so only names and course adjustments change.
func (a *App) SaveSchoolCalendarExceptions(ctx context.Context, in SchoolCalendarExceptionsInput, actor string) (SchoolCalendarAdminView, error) {
	college := strings.TrimSpace(in.College)
	if len(college) > 128 || strings.ContainsAny(college, "\r\n\x00") {
		return SchoolCalendarAdminView{}, Err("invalid_college", "学院名称无效", http.StatusBadRequest)
	}
	if len(in.Exceptions) > 1000 {
		return SchoolCalendarAdminView{}, Err("invalid_school_calendar", "例外日期数量无效", http.StatusBadRequest)
	}
	base, err := a.GetSchoolCalendar(ctx)
	if err != nil {
		return SchoolCalendarAdminView{}, err
	}
	days := make([]SchoolCalendarDay, len(base.Days))
	copy(days, base.Days)
	for i := range days {
		days[i].Name, days[i].Adjustment = "", ""
	}
	index := make(map[string]int, len(days))
	for i, day := range days {
		index[day.Date] = i
	}
	seen := make(map[string]bool, len(in.Exceptions))
	for _, exception := range in.Exceptions {
		_, normalizedDate, dateErr := parseSchoolCalendarDate(exception.Date)
		if dateErr != nil {
			return SchoolCalendarAdminView{}, Err("invalid_school_calendar", "例外日期格式无效", http.StatusBadRequest)
		}
		position, ok := index[normalizedDate]
		if !ok {
			return SchoolCalendarAdminView{}, Err("invalid_school_calendar", "例外日期必须位于当前校历范围内", http.StatusBadRequest)
		}
		if seen[normalizedDate] {
			return SchoolCalendarAdminView{}, Err("invalid_school_calendar", "例外日期不能重复", http.StatusBadRequest)
		}
		seen[normalizedDate] = true
		name := strings.TrimSpace(exception.Name)
		if len(name) > 64 || strings.ContainsAny(name, "\r\n\x00") {
			return SchoolCalendarAdminView{}, Err("invalid_school_calendar", "校历名称无效", http.StatusBadRequest)
		}
		adjustment := strings.TrimSpace(exception.Adjustment)
		if adjustment != "" && adjustment != "/" {
			if !schoolCalendarAdjustmentPattern.MatchString(adjustment) {
				return SchoolCalendarAdminView{}, Err("invalid_school_calendar", "课程调整日期格式无效", http.StatusBadRequest)
			}
			if _, parseErr := time.Parse("20060102", adjustment); parseErr != nil {
				return SchoolCalendarAdminView{}, Err("invalid_school_calendar", "课程调整日期无效", http.StatusBadRequest)
			}
		}
		days[position].Name, days[position].Adjustment = name, adjustment
	}
	encoded, err := json.Marshal(days)
	if err != nil {
		return SchoolCalendarAdminView{}, err
	}
	now := time.Now().UTC()
	if college == "" {
		encodedSlots, slots, slotErr := validateSchoolCalendarSlots(base.Slots)
		if slotErr != nil {
			return SchoolCalendarAdminView{}, slotErr
		}
		if _, err := a.Store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{
			DaysJSON: string(encoded), SlotsJSON: encodedSlots, UpdatedAt: now,
		}); err != nil {
			return SchoolCalendarAdminView{}, err
		}
		_ = a.Store.AddAudit(ctx, actor, "school_calendar_exceptions", "school_calendar", "global",
			controlcrypto.JSON(map[string]any{"exceptions": len(in.Exceptions), "slots": len(slots)}), now)
	} else {
		if err := a.Store.SaveSchoolCalendarCollege(ctx, college, string(encoded), now); err != nil {
			return SchoolCalendarAdminView{}, err
		}
		a.touchSchoolCalendarVersion(ctx, now)
		_ = a.Store.AddAudit(ctx, actor, "school_calendar_exceptions", "school_calendar", college,
			controlcrypto.JSON(map[string]any{"exceptions": len(in.Exceptions)}), now)
	}
	return a.GetSchoolCalendarAdmin(ctx)
}

// touchSchoolCalendarVersion bumps the global calendar version so clients
// notice a change in a college override and refetch their scoped calendar.
func (a *App) touchSchoolCalendarVersion(ctx context.Context, now time.Time) {
	config, err := a.Store.GetSchoolCalendarConfig(ctx)
	if err != nil {
		return
	}
	if _, err := a.Store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{
		DaysJSON: config.DaysJSON, SlotsJSON: config.SlotsJSON, UpdatedAt: now,
	}); err != nil {
		a.Logger.Warn("touch school calendar version failed", "error", err)
	}
}

// DeleteSchoolCalendarExceptions removes a college override so it falls back to
// the global calendar.
func (a *App) DeleteSchoolCalendarExceptions(ctx context.Context, college, actor string) (SchoolCalendarAdminView, error) {
	college = strings.TrimSpace(college)
	if college == "" {
		return SchoolCalendarAdminView{}, Err("invalid_college", "学院名称无效", http.StatusBadRequest)
	}
	if err := a.Store.DeleteSchoolCalendarCollege(ctx, college); err != nil {
		return SchoolCalendarAdminView{}, err
	}
	a.touchSchoolCalendarVersion(ctx, time.Now().UTC())
	_ = a.Store.AddAudit(ctx, actor, "school_calendar_exception_clear", "school_calendar", college, "{}", time.Now().UTC())
	return a.GetSchoolCalendarAdmin(ctx)
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
