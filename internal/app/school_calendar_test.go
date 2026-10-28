package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"xzitpocket-control/internal/store/sqlite"
)

func TestCollegeCalendarExceptionsReplaceGlobal(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	a := &App{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	global, err := a.GetSchoolCalendar(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var first, second string
	for _, day := range global.Days {
		if day.Name == "" {
			if first == "" {
				first = day.Date
			} else if second == "" {
				second = day.Date
				break
			}
		}
	}

	if _, err := a.SaveSchoolCalendarExceptions(ctx, SchoolCalendarExceptionsInput{
		College: "数学与统计学院",
		Exceptions: []SchoolCalendarDay{
			{Date: first, Name: "院级活动"},
		},
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	college, err := a.GetSchoolCalendarForCollege(ctx, "数学与统计学院")
	if err != nil {
		t.Fatal(err)
	}
	named := ""
	for _, day := range college.Days {
		if day.Date == first {
			named = day.Name
		}
		if day.Date == second && day.Name != "" {
			t.Fatalf("college calendar kept a global exception: %#v", day)
		}
	}
	if named != "院级活动" {
		t.Fatalf("college exception not applied: %q", named)
	}

	// Another college and the anonymous client keep the global calendar.
	other, err := a.GetSchoolCalendarForCollege(ctx, "机电工程学院")
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range other.Days {
		if day.Date == first && day.Name == "院级活动" {
			t.Fatal("global calendar leaked the college override")
		}
	}

	// Out-of-range exception dates are rejected.
	if _, err := a.SaveSchoolCalendarExceptions(ctx, SchoolCalendarExceptionsInput{
		College:    "数学与统计学院",
		Exceptions: []SchoolCalendarDay{{Date: "1999-01-01", Name: "越界"}},
	}, "admin"); err == nil {
		t.Fatal("an out-of-range exception should be rejected")
	}

	admin, err := a.GetSchoolCalendarAdmin(ctx)
	if err != nil || len(admin.Colleges) != 1 || admin.Colleges[0].College != "数学与统计学院" {
		t.Fatalf("admin view = %#v err=%v", admin, err)
	}

	if _, err := a.DeleteSchoolCalendarExceptions(ctx, "数学与统计学院", "admin"); err != nil {
		t.Fatal(err)
	}
	back, err := a.GetSchoolCalendarForCollege(ctx, "数学与统计学院")
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range back.Days {
		if day.Date == first && day.Name == "院级活动" {
			t.Fatal("override was not cleared")
		}
	}
}

func TestGetSchoolCalendarReinitializesCorruptConfig(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "UPDATE school_calendar_config SET days_json = ?", "[{\"date\":\"not-a-date\"}]"); err != nil {
		t.Fatal(err)
	}

	calendar, err := (&App{Store: store}).GetSchoolCalendar(ctx)
	if err != nil {
		t.Fatalf("GetSchoolCalendar returned an error: %v", err)
	}
	if len(calendar.Days) == 0 || calendar.UpdatedAt == "" {
		t.Fatalf("reinitialized calendar = %#v", calendar)
	}
	var stored string
	if err := store.DB.QueryRowContext(ctx, "SELECT days_json FROM school_calendar_config WHERE id = 1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStoredSchoolCalendar(stored); err != nil {
		t.Fatalf("stored calendar remains invalid: %v", err)
	}
	versions, err := (&App{Store: store}).GetConfigVersions(ctx)
	if err != nil || versions.SchoolCalendar != calendar.UpdatedAt {
		t.Fatalf("reset calendar version = %q, response = %q: %v", versions.SchoolCalendar, calendar.UpdatedAt, err)
	}
}

func TestSchoolCalendarSlotsRoundTrip(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	a := &App{Store: store}

	// Empty slots fall back to the built-in 14-period schedule.
	view, err := a.GetSchoolCalendar(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Slots) != 14 || view.Slots[0].Start != "08:00" || view.Slots[13].End != "21:20" {
		t.Fatalf("default slots = %#v", view.Slots)
	}
	if !view.Slots[4].Lab || !view.Slots[5].Lab || view.Slots[0].Lab {
		t.Fatalf("default lab flags = %#v", view.Slots)
	}

	custom := []SchoolCalendarSlot{
		{Index: 99, Start: "07:30", End: "08:15", Lab: true},
		{Index: 1, Start: "08:25", End: "09:10"},
	}
	out, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{Days: view.Days, Slots: custom}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	// Indices are renumbered 1..N regardless of the submitted values.
	if len(out.Slots) != 2 || out.Slots[0].Index != 1 || out.Slots[1].Index != 2 || out.Slots[0].Start != "07:30" || !out.Slots[0].Lab || out.Slots[1].Lab {
		t.Fatalf("saved slots = %#v", out.Slots)
	}
	got, err := a.GetSchoolCalendar(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Slots) != 2 || got.Slots[1].End != "09:10" || !got.Slots[0].Lab {
		t.Fatalf("reloaded slots = %#v", got.Slots)
	}

	if _, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{Days: view.Days, Slots: []SchoolCalendarSlot{{Start: "09:00", End: "09:00"}}}, "admin"); err == nil {
		t.Fatal("expected equal start/end slot to be rejected")
	}
	if _, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{Days: view.Days, Slots: []SchoolCalendarSlot{{Start: "9:00", End: "10:00"}}}, "admin"); err == nil {
		t.Fatal("expected non-padded slot time to be rejected")
	}
}
