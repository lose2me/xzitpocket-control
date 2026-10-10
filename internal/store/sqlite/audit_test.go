package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupAuditLogs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.AddAudit(ctx, "usr-1", "login_attempt", "device", "dev-1", "{}", now.AddDate(0, 0, -40)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAudit(ctx, "usr-1", "login_attempt", "device", "dev-1", "{}", now.AddDate(0, 0, -10)); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.CleanupAuditLogs(ctx, now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	items, err := store.ListAudit(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("remaining = %d, want 1", len(items))
	}
}
