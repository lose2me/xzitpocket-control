package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrateCreatesCurrentSchema(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	for _, table := range []string{"question_banks", "questions", "question_options", "question_bank_id_counter", "library_cdks", "error_reports", "error_report_ignored_students"} {
		if err := store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s table is missing", table)
		}
	}
}

func TestMetricsHandleAnonymousEventsAndCalendarWindows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	user := User{ID: "usr_test", Status: "active", CreatedAt: now, LastLoginAt: now}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	device := Device{ID: "dev_test", DeviceSerial: "dev_serial", Installation: "installation", TokenHash: "token", PublicKey: "public", Platform: "android", AppVersion: "1.0.0", CreatedAt: now, LastSeenAt: now}
	if err := store.CreateDevice(context.Background(), device); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.InsertEvents(context.Background(), []EventInput{
		{EventID: "calendar-window", UserID: user.ID, DeviceID: device.ID, Type: "app_start", OccurredAt: dayStart.AddDate(0, 0, -6).Add(time.Hour), ReceivedAt: now, Properties: `{"platform":"android"}`},
		{EventID: "anonymous", DeviceID: device.ID, Type: "app_start", OccurredAt: now.Add(-time.Hour), ReceivedAt: now, Properties: `{"platform":"android","app_version":"1.0.0"}`},
		{EventID: "library", UserID: user.ID, DeviceID: device.ID, Type: "library_open", OccurredAt: now.Add(-time.Hour), ReceivedAt: now, Properties: `{"library":"main"}`},
	})
	if err != nil {
		t.Fatal(err)
	}

	overview, err := store.MetricsOverview(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if overview.WAU != 1 {
		t.Fatalf("WAU = %d, want 1 for the full seven-calendar-day window", overview.WAU)
	}

	breakdown, err := store.MetricsBreakdown(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if breakdown.AnonymousEvents != 1 {
		t.Fatalf("anonymous events = %d, want 1", breakdown.AnonymousEvents)
	}
	if breakdown.LibraryEntries != 1 || breakdown.LibraryUsers != 1 {
		t.Fatalf("library metrics = (%d, %d), want (1, 1)", breakdown.LibraryEntries, breakdown.LibraryUsers)
	}
	if breakdown.Platforms["android"] != 1 || breakdown.Versions["1.0.0"] != 1 {
		t.Fatalf("device distributions = platforms %#v, versions %#v; want one device", breakdown.Platforms, breakdown.Versions)
	}
}

func TestMetricsUseShanghaiCalendarDay(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// 16:00 UTC is midnight in Asia/Shanghai. Only the second event belongs
	// to September 2 on the control dashboard.
	now := time.Date(2026, 9, 1, 16, 30, 0, 0, time.UTC)
	for _, item := range []struct {
		userID, deviceID, suffix, eventType string
		occurred                            time.Time
	}{
		{userID: "usr-before-midnight", deviceID: "dev-before-midnight", suffix: "before", eventType: "app_start", occurred: time.Date(2026, 9, 1, 15, 59, 0, 0, time.UTC)},
		{userID: "usr-after-midnight", deviceID: "dev-after-midnight", suffix: "after", eventType: "app_start", occurred: time.Date(2026, 9, 1, 16, 1, 0, 0, time.UTC)},
		{userID: "usr-logout-only", deviceID: "dev-logout-only", suffix: "logout", eventType: "logout", occurred: time.Date(2026, 9, 1, 16, 2, 0, 0, time.UTC)},
		{userID: "usr-old-device", deviceID: "dev-old-device", suffix: "old", eventType: "app_start", occurred: time.Date(2026, 8, 29, 15, 59, 0, 0, time.UTC)},
	} {
		if err := store.CreateUser(ctx, User{ID: item.userID, Status: "active", CreatedAt: item.occurred, LastLoginAt: item.occurred}); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateDevice(ctx, Device{ID: item.deviceID, DeviceSerial: "serial-calendar-" + item.suffix, Installation: "install-calendar-" + item.suffix, TokenHash: "token-calendar-" + item.suffix, PublicKey: "key", CreatedAt: item.occurred, LastSeenAt: item.occurred}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.InsertEvents(ctx, []EventInput{{EventID: "event-calendar-" + item.suffix, UserID: item.userID, DeviceID: item.deviceID, Type: item.eventType, OccurredAt: item.occurred, ReceivedAt: now, Properties: `{}`}}); err != nil {
			t.Fatal(err)
		}
	}

	overview, err := store.MetricsOverview(ctx, now)
	if err != nil || overview.DAU != 1 || overview.ActiveDevices != 3 || overview.TodayEvents != 2 {
		t.Fatalf("Shanghai day overview = %#v, %v", overview, err)
	}
	series, err := store.MetricsSeries(ctx, 1, now)
	if err != nil || len(series) != 1 || series[0].Day != "2026-09-02" || series[0].TotalUsers != 4 || series[0].DAU != 1 || series[0].WAU != 3 || series[0].TodayEvents != 2 {
		t.Fatalf("Shanghai day series = %#v, %v", series, err)
	}
}

func TestMetricsBreakdownCountsCDKActivations(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	if err := store.CreateQuestionBank(ctx, QuestionBank{ID: "QB-CDK-METRICS", Name: "CDK 统计", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	todayUsed := now.Add(-time.Hour)
	weekUsed := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	oldUsed := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	if err := store.CreateLibraryCDKs(ctx, []LibraryCDK{
		{ID: "cdk-metrics-today", CodeHash: "metrics-today", Status: "used", CreatedAt: now, UsedAt: &todayUsed},
		{ID: "cdk-metrics-week", CodeHash: "metrics-week", Status: "used", CreatedAt: now, UsedAt: &weekUsed},
		{ID: "cdk-metrics-old", CodeHash: "metrics-old", Status: "used", CreatedAt: now, UsedAt: &oldUsed},
		{ID: "cdk-metrics-active", CodeHash: "metrics-active", Status: "active", CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	breakdown, err := store.MetricsBreakdown(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if breakdown.CDKActivationsToday != 1 || breakdown.CDKActivationsWeek != 2 || breakdown.CDKActivationsTotal != 3 {
		t.Fatalf("CDK activation metrics = today %d, week %d, total %d; want 1, 2, 3", breakdown.CDKActivationsToday, breakdown.CDKActivationsWeek, breakdown.CDKActivationsTotal)
	}
}
