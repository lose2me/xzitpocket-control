package app

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func TestConfigVersionsTrackSameMillisecondWrites(t *testing.T) {
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
	// Force subsequent writes into the same stored millisecond, independently
	// of how quickly the test runner executes them.
	base := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Add(100 * time.Millisecond)
	if _, err := store.UpdateAppReleaseConfig(ctx, sqlite.AppReleaseConfig{UpdatedAt: base}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSchoolCalendarConfig(ctx, sqlite.SchoolCalendarConfig{DaysJSON: "[]", UpdatedAt: base}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		release, err := a.UpdateAppReleaseConfig(ctx, AppReleaseInput{
			LatestVersion: "2.1." + strconv.Itoa(i), DownloadURL: "https://con.xuda.live/app.apk",
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		calendar, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{Days: []SchoolCalendarDay{
			{Date: "2026-10-06", Name: "校历" + strconv.Itoa(i)},
		}}, "")
		if err != nil {
			t.Fatal(err)
		}
		versions, err := a.GetConfigVersions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := base.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano)
		if release.UpdatedAt != want || calendar.UpdatedAt != want || versions.AppRelease != want || versions.SchoolCalendar != want {
			t.Fatalf("write %d: release=%q calendar=%q versions=%#v, want %q", i, release.UpdatedAt, calendar.UpdatedAt, versions, want)
		}
		storedRelease, err := a.GetAppReleaseConfig(ctx)
		if err != nil || storedRelease != release {
			t.Fatalf("release update response differs from stored response: %#v %#v %v", release, storedRelease, err)
		}
		storedCalendar, err := a.GetSchoolCalendar(ctx)
		if err != nil || storedCalendar.UpdatedAt != calendar.UpdatedAt || storedCalendar.Days[0].Name != calendar.Days[0].Name {
			t.Fatalf("calendar update response differs from stored response: %#v %#v %v", calendar, storedCalendar, err)
		}
	}
}

func TestQuestionBankVersionsTrackSameMillisecondWrites(t *testing.T) {
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
	input := QuestionBankInput{Name: "题库甲", Questions: []QuestionInput{
		{QuestionNumber: 1, Type: "填空题", Title: "第1题", QuestionText: "题干", CorrectAnswer: "答案"},
	}}
	created, err := a.CreateQuestionBank(ctx, input, "")
	if err != nil {
		t.Fatal(err)
	}
	id := created.QuestionBank.ID
	assertVersion := func(want string) {
		t.Helper()
		admin, err := a.GetAdminQuestionBank(ctx, id)
		if err != nil || admin.UpdatedAt != want {
			t.Fatalf("admin version = %q, want %q: %v", admin.UpdatedAt, want, err)
		}
		items, _, err := a.ListQuestionBanksForUser(ctx, 100, 0, "")
		if err != nil || len(items) != 1 || items[0].UpdatedAt != want {
			t.Fatalf("user summaries = %#v, want version %q: %v", items, want, err)
		}
		items, _, err = a.ListQuestionBanks(ctx, 100, 0, "")
		if err != nil || len(items) != 1 || items[0].UpdatedAt != want {
			t.Fatalf("admin summaries = %#v, want version %q: %v", items, want, err)
		}
	}
	assertVersion(created.UpdatedAt)
	bank, err := store.GetQuestionBank(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Add(100 * time.Millisecond)
	if _, err := store.DB.ExecContext(ctx, "UPDATE question_banks SET updated_at = ? WHERE id = ?", base.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
	bank.Name = "题库乙"
	bank.UpdatedAt = base.Add(100 * time.Microsecond)
	if err := store.UpdateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	want := base.Add(time.Millisecond).Format(time.RFC3339Nano)
	assertVersion(want)
	bank.UpdatedAt = base.Add(time.Hour)
	if err := store.UpdateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	assertVersion(want) // Unchanged content keeps the cache version.
	if err := store.SetQuestionBankStatus(ctx, id, "draft", base); err != nil {
		t.Fatal(err)
	}
	if err := store.SetQuestionBankStatus(ctx, id, "active", base); err != nil {
		t.Fatal(err)
	}
	want = base.Add(3 * time.Millisecond).Format(time.RFC3339Nano)
	assertVersion(want)
	if err := store.SetQuestionBankStatus(ctx, id, "active", base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertVersion(want) // Reapplying the same status also keeps the version.
}
