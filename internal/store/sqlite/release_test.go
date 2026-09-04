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
	if err := store.Migrate(ctx); err != nil {
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
	want := AppReleaseConfig{LatestVersion: "2.0.4", DownloadURL: "https://download.example.test/app.apk", UpdatedAt: now}
	if err := store.UpdateAppReleaseConfig(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetAppReleaseConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestVersion != want.LatestVersion || got.DownloadURL != want.DownloadURL || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("release config mismatch: got=%#v want=%#v", got, want)
	}
}
