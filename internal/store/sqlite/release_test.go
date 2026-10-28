package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAppReleaseConfigRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetAppReleaseConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.LatestVersion != "" || initial.DownloadURL != "" || !initial.UpdatedAt.IsZero() {
		t.Fatalf("unexpected initial release config: %#v", initial)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	want := AppReleaseConfig{LatestVersion: "2.0.4", DownloadURL: "https://download.example.test/app.apk", Changelog: "## 新增\n- 更新日志", UpdatedAt: now}
	if _, err := store.UpdateAppReleaseConfig(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetAppReleaseConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestVersion != want.LatestVersion || got.DownloadURL != want.DownloadURL || got.Changelog != want.Changelog || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("release config mismatch: got=%#v want=%#v", got, want)
	}
}

func TestAppReleaseTestConfigIsIndependent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	prod := AppReleaseConfig{LatestVersion: "2.0.4", DownloadURL: "https://download.example.test/app.apk", Changelog: "正式日志", UpdatedAt: now}
	test := AppReleaseConfig{LatestVersion: "2.1.0", DownloadURL: "https://download.example.test/test.apk", Changelog: "测试日志", UpdatedAt: now}
	if _, err := store.UpdateAppReleaseConfig(ctx, prod); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAppReleaseTestConfig(ctx, test); err != nil {
		t.Fatal(err)
	}
	gotProd, err := store.GetAppReleaseConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotTest, err := store.GetAppReleaseTestConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotProd.LatestVersion != "2.0.4" || gotProd.Changelog != "正式日志" {
		t.Fatalf("production config changed unexpectedly: %#v", gotProd)
	}
	if gotTest.LatestVersion != "2.1.0" || gotTest.Changelog != "测试日志" {
		t.Fatalf("test config mismatch: %#v", gotTest)
	}
}
