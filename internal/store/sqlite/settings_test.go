package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsSeedAndUpdate(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	if err := store.SeedSettings(ctx, map[string]string{"A": "1", "B": "2"}, now); err != nil {
		t.Fatal(err)
	}
	// Seeding keeps existing rows.
	if err := store.SeedSettings(ctx, map[string]string{"A": "9", "C": "3"}, now); err != nil {
		t.Fatal(err)
	}
	values, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values["A"] != "1" || values["B"] != "2" || values["C"] != "3" {
		t.Fatalf("seeded settings = %#v", values)
	}

	if err := store.UpdateSettings(ctx, map[string]string{"A": "10", "D": "4"}, now); err != nil {
		t.Fatal(err)
	}
	values, err = store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if values["A"] != "10" || values["D"] != "4" {
		t.Fatalf("updated settings = %#v", values)
	}
}
