package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestGenericLibraryCDKRedemptionAndAccess(t *testing.T) {
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
	if err := store.CreateQuestionBank(ctx, QuestionBank{ID: "QB-001", Name: "题库", Status: "active", RequiresCDK: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cdk := LibraryCDK{ID: "cdk-1", CodeHash: "hash-1", Status: "active", CreatedAt: now}
	if err := store.CreateLibraryCDK(ctx, cdk); err != nil {
		t.Fatal(err)
	}
	used, err := store.RedeemLibraryCDK(ctx, cdk.CodeHash, "QB-001", "student-a", "cipher-a", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if used.Status != "used" || used.BoundStudentIDHash != "student-a" {
		t.Fatalf("unexpected redemption: %#v", used)
	}
	allowed, err := store.HasLibraryAccess(ctx, "QB-001", "student-a")
	if err != nil || !allowed {
		t.Fatalf("generic access = %v, %v", allowed, err)
	}
	allowed, err = store.HasLibraryAccess(ctx, "QB-001", "student-b")
	if err != nil || allowed {
		t.Fatalf("unbound access = %v, %v", allowed, err)
	}
	if _, err := store.RedeemLibraryCDK(ctx, cdk.CodeHash, "QB-001", "student-b", "cipher-b", "", now.Add(2*time.Minute)); !errors.Is(err, ErrLibraryCDKBound) {
		t.Fatalf("different-student error = %v", err)
	}
	if err := store.SetLibraryCDKStatus(ctx, cdk.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RedeemLibraryCDK(ctx, cdk.CodeHash, "QB-001", "student-a", "cipher-a", "user-a", now.Add(3*time.Minute)); !errors.Is(err, ErrLibraryCDKDisabled) {
		t.Fatalf("disabled redemption error = %v", err)
	}
	allowed, err = store.HasLibraryAccess(ctx, "QB-001", "student-a")
	if err != nil || allowed {
		t.Fatalf("disabled bound CDK should be unavailable: %v, %v", allowed, err)
	}
	if err := store.SetLibraryCDKStatus(ctx, cdk.ID, "active"); err != nil {
		t.Fatal(err)
	}
	allowed, err = store.HasLibraryAccess(ctx, "QB-001", "student-a")
	if err != nil || !allowed {
		t.Fatalf("re-enabled bound CDK should restore access: %v, %v", allowed, err)
	}
}

func TestLibraryCDKConcurrentRedemptionBindsOnce(t *testing.T) {
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
	if err := store.CreateQuestionBank(ctx, QuestionBank{ID: "QB-001", Name: "题库", Status: "active", RequiresCDK: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateLibraryCDK(ctx, LibraryCDK{ID: "cdk-concurrent", CodeHash: "hash-concurrent", Status: "active", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, student := range []string{"student-a", "student-b"} {
		go func(student string) {
			_, err := store.RedeemLibraryCDK(ctx, "hash-concurrent", "QB-001", student, "", "", now)
			results <- err
		}(student)
	}
	var success, bound int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			success++
		case errors.Is(err, ErrLibraryCDKBound):
			bound++
		default:
			t.Fatalf("unexpected redemption error: %v", err)
		}
	}
	if success != 1 || bound != 1 {
		t.Fatalf("success=%d bound=%d", success, bound)
	}
}

func TestLibraryCDKSearch(t *testing.T) {
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
	if err := store.CreateLibraryCDKs(ctx, []LibraryCDK{{ID: "cdk-search-1", CodeHash: "search-hash-1", Status: "active", CreatedAt: now}, {ID: "cdk-search-2", CodeHash: "search-hash-2", Status: "used", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	items, total, err := store.ListLibraryCDKs(ctx, 50, 0, "cdk-search-1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != "cdk-search-1" {
		t.Fatalf("unexpected CDK search: %d %#v", total, items)
	}
}
