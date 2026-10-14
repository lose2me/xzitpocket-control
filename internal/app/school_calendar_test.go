package app

import (
	"context"
	"path/filepath"
	"testing"

	"xzitpocket-control/internal/store/sqlite"
)

func TestGetSchoolCalendarReinitializesCorruptConfig(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
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
	if err := store.Migrate(ctx); err != nil {
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

	custom := []SchoolCalendarSlot{
		{Index: 99, Start: "07:30", End: "08:15"},
		{Index: 1, Start: "08:25", End: "09:10"},
	}
	out, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{Days: view.Days, Slots: custom}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	// Indices are renumbered 1..N regardless of the submitted values.
	if len(out.Slots) != 2 || out.Slots[0].Index != 1 || out.Slots[1].Index != 2 || out.Slots[0].Start != "07:30" {
		t.Fatalf("saved slots = %#v", out.Slots)
	}
	got, err := a.GetSchoolCalendar(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Slots) != 2 || got.Slots[1].End != "09:10" {
		t.Fatalf("reloaded slots = %#v", got.Slots)
	}

	if _, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{Days: view.Days, Slots: []SchoolCalendarSlot{{Start: "09:00", End: "09:00"}}}, "admin"); err == nil {
		t.Fatal("expected equal start/end slot to be rejected")
	}
	if _, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{Days: view.Days, Slots: []SchoolCalendarSlot{{Start: "9:00", End: "10:00"}}}, "admin"); err == nil {
		t.Fatal("expected non-padded slot time to be rejected")
	}
}
