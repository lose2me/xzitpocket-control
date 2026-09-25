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

var schoolCalendarDatePattern = regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})$`)
var schoolCalendarAdjustmentPattern = regexp.MustCompile(`^\d{8}$`)

type SchoolCalendarDay struct {
	Date       string `json:"date"`
	Weekday    int    `json:"weekday"`
	Holiday    bool   `json:"holiday"`
	Festival   any    `json:"festival,omitempty"`
	Adjustment string `json:"adjustment,omitempty"`
}

type SchoolCalendarInput struct {
	Days []SchoolCalendarDay `json:"days"`
}

type SchoolCalendarView struct {
	Days      []SchoolCalendarDay `json:"days"`
	UpdatedAt string              `json:"updatedAt,omitempty"`
}

func validateSchoolCalendarInput(in SchoolCalendarInput) (string, []SchoolCalendarDay, error) {
	if len(in.Days) == 0 || len(in.Days) > 1000 {
		return "", nil, Err("invalid_school_calendar", "校历日期数量无效", http.StatusBadRequest)
	}
	seen := make(map[string]bool, len(in.Days))
	for i := range in.Days {
		day := &in.Days[i]
		day.Date = strings.TrimSpace(day.Date)
		parsed, normalizedDate, err := parseSchoolCalendarDate(day.Date)
		if err != nil {
			return "", nil, Err("invalid_school_calendar", "校历日期格式无效", http.StatusBadRequest)
		}
		day.Date = normalizedDate
		if day.Weekday < 1 || day.Weekday > 7 {
			return "", nil, Err("invalid_school_calendar", "校历星期格式无效", http.StatusBadRequest)
		}
		weekday := (int(parsed.Weekday())+6)%7 + 1
		if day.Weekday != weekday {
			return "", nil, Err("invalid_school_calendar", "校历星期与日期不一致", http.StatusBadRequest)
		}
		if seen[day.Date] {
			return "", nil, Err("invalid_school_calendar", "校历日期不能重复", http.StatusBadRequest)
		}
		seen[day.Date] = true
		switch festival := day.Festival.(type) {
		case nil:
			day.Festival = ""
		case bool:
			if festival {
				return "", nil, Err("invalid_school_calendar", "校历节日名称无效", http.StatusBadRequest)
			}
			day.Festival = ""
		case string:
			day.Festival = strings.TrimSpace(festival)
		default:
			return "", nil, Err("invalid_school_calendar", "校历节日名称无效", http.StatusBadRequest)
		}
		festival, _ := day.Festival.(string)
		if len(festival) > 64 || strings.ContainsAny(festival, "\r\n\x00") {
			return "", nil, Err("invalid_school_calendar", "校历节日名称无效", http.StatusBadRequest)
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

func schoolCalendarView(config sqlite.SchoolCalendarConfig, days []SchoolCalendarDay) SchoolCalendarView {
	out := SchoolCalendarView{Days: days}
	if !config.UpdatedAt.IsZero() {
		out.UpdatedAt = config.UpdatedAt.Format(time.RFC3339)
	}
	return out
}

func decodeStoredSchoolCalendar(raw string) ([]SchoolCalendarDay, error) {
	var days []SchoolCalendarDay
	if err := json.Unmarshal([]byte(raw), &days); err != nil {
		return nil, err
	}
	return days, nil
}

func defaultSchoolCalendarDays() []SchoolCalendarDay {
	start := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, time.January, 10, 0, 0, 0, 0, time.UTC)
	festivals := map[string]string{
		"2026-09-25": "中秋",
		"2026-10-01": "国庆",
		"2027-01-01": "元旦",
	}
	extraHolidays := map[string]bool{
		"2026-09-25": true,
		"2026-10-01": true,
		"2026-10-02": true,
		"2026-10-05": true,
		"2026-10-06": true,
		"2026-10-07": true,
		"2027-01-01": true,
	}
	days := make([]SchoolCalendarDay, 0, int(end.Sub(start)/(24*time.Hour))+1)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		weekday := (int(date.Weekday())+6)%7 + 1
		days = append(days, SchoolCalendarDay{
			Date:     key,
			Weekday:  weekday,
			Holiday:  weekday >= 6 || extraHolidays[key],
			Festival: festivals[key],
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
	if err != nil {
		return SchoolCalendarView{}, Err("invalid_school_calendar", "校历配置损坏", http.StatusInternalServerError)
	}
	if len(days) == 0 {
		days = defaultSchoolCalendarDays()
	} else {
		_, normalized, validationErr := validateSchoolCalendarInput(SchoolCalendarInput{Days: days})
		if validationErr != nil {
			return SchoolCalendarView{}, Err("invalid_school_calendar", "校历配置损坏", http.StatusInternalServerError)
		}
		days = normalized
	}
	return schoolCalendarView(config, days), nil
}

func (a *App) UpdateSchoolCalendar(ctx context.Context, in SchoolCalendarInput, actor string) (SchoolCalendarView, error) {
	encoded, days, err := validateSchoolCalendarInput(in)
	if err != nil {
		return SchoolCalendarView{}, err
	}
	now := time.Now().UTC()
	if err := a.Store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{DaysJSON: encoded, UpdatedAt: now}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SchoolCalendarView{}, ErrNotFound
		}
		return SchoolCalendarView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "school_calendar_update", "school_calendar", "current",
		controlcrypto.JSON(map[string]any{"days": len(days)}), now)
	return schoolCalendarView(sqlite.SchoolCalendarConfig{DaysJSON: encoded, UpdatedAt: now}, days), nil
}
