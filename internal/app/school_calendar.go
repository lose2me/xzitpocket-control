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

type SchoolCalendarDay struct {
	Date       string `json:"date"`
	Name       string `json:"name"`
	Adjustment string `json:"adjustment"`
}

type SchoolCalendarInput struct {
	Days []SchoolCalendarDay `json:"days"`
}

type SchoolCalendarView struct {
	Days      []SchoolCalendarDay `json:"days"`
	UpdatedAt string              `json:"updatedAt"`
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
	if err != nil {
		return a.resetSchoolCalendar(ctx)
	}
	if len(days) == 0 {
		return a.resetSchoolCalendar(ctx)
	} else {
		_, normalized, validationErr := validateSchoolCalendarInput(SchoolCalendarInput{Days: days})
		if validationErr != nil {
			return a.resetSchoolCalendar(ctx)
		}
		days = normalized
	}
	return schoolCalendarView(config, days), nil
}

func (a *App) resetSchoolCalendar(ctx context.Context) (SchoolCalendarView, error) {
	encoded, days, err := validateSchoolCalendarInput(SchoolCalendarInput{Days: defaultSchoolCalendarDays()})
	if err != nil {
		return SchoolCalendarView{}, err
	}
	now := time.Now().UTC()
	config, err := a.Store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{DaysJSON: encoded, UpdatedAt: now})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SchoolCalendarView{}, ErrNotFound
		}
		return SchoolCalendarView{}, err
	}
	return schoolCalendarView(config, days), nil
}

func (a *App) UpdateSchoolCalendar(ctx context.Context, in SchoolCalendarInput, actor string) (SchoolCalendarView, error) {
	encoded, days, err := validateSchoolCalendarInput(in)
	if err != nil {
		return SchoolCalendarView{}, err
	}
	now := time.Now().UTC()
	config, err := a.Store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{DaysJSON: encoded, UpdatedAt: now})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SchoolCalendarView{}, ErrNotFound
		}
		return SchoolCalendarView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "school_calendar_update", "school_calendar", "current",
		controlcrypto.JSON(map[string]any{"days": len(days)}), now)
	return schoolCalendarView(config, days), nil
}
