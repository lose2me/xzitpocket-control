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
