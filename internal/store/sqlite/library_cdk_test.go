package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestQuestionBankAutoIDAndLibraryCDKRedemption(t *testing.T) {
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
	first, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "一", Status: "active", RequiresCDK: true, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "二", Status: "active", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "QB-001" || second.ID != "QB-002" {
		t.Fatalf("unexpected generated IDs: %q, %q", first.ID, second.ID)
	}
	if err := store.CreateUser(ctx, User{ID: "user-a", Status: "active", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	cdk := LibraryCDK{ID: "cdk-1", CodeHash: "hash-1", QuestionBankID: first.ID, Status: "active", CreatedAt: now}
	if err := store.CreateLibraryCDK(ctx, cdk); err != nil {
		t.Fatal(err)
	}
	used, err := store.RedeemLibraryCDK(ctx, cdk.CodeHash, "student-a", "cipher-a", "user-a", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if used.Status != "used" || used.BoundStudentIDHash != "student-a" {
		t.Fatalf("unexpected redeemed CDK: %#v", used)
	}
	// A retry from the same student is intentionally idempotent.
	if _, err := store.RedeemLibraryCDK(ctx, cdk.CodeHash, "student-a", "cipher-a", "user-a", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("same-student redemption should be idempotent: %v", err)
	}
	if _, err := store.RedeemLibraryCDK(ctx, cdk.CodeHash, "student-b", "cipher-b", "user-b", now.Add(3*time.Minute)); err != ErrLibraryCDKBound {
		t.Fatalf("different-student redemption error = %v, want %v", err, ErrLibraryCDKBound)
	}
	allowed, err := store.HasLibraryAccess(ctx, first.ID, "student-a")
	if err != nil || !allowed {
		t.Fatalf("bound student access = %v, %v", allowed, err)
	}
	allowed, err = store.HasLibraryAccess(ctx, first.ID, "student-b")
	if err != nil || allowed {
		t.Fatalf("unbound student access = %v, %v", allowed, err)
	}
	if err := store.SetLibraryCDKStatus(ctx, cdk.ID, "disabled"); err != nil {
		t.Fatalf("disable used CDK: %v", err)
	}
	if _, err := store.RedeemLibraryCDK(ctx, cdk.CodeHash, "student-a", "cipher-a", "user-a", now.Add(5*time.Minute)); err != ErrLibraryCDKDisabled {
		t.Fatalf("disabled CDK redemption error = %v, want %v", err, ErrLibraryCDKDisabled)
	}
	if err := store.SetLibraryCDKStatus(ctx, cdk.ID, "active"); err != nil {
		t.Fatalf("enable used CDK: %v", err)
	}
	allowed, err = store.HasLibraryAccess(ctx, first.ID, "student-a")
	if err != nil || !allowed {
		t.Fatalf("re-enabled used CDK access = %v, %v", allowed, err)
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
	if err := store.CreateQuestionBank(ctx, QuestionBank{ID: "QB-001", Name: "并发", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateLibraryCDK(ctx, LibraryCDK{ID: "cdk-concurrent", CodeHash: "hash-concurrent", QuestionBankID: "QB-001", Status: "active", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, student := range []string{"student-a", "student-b"} {
		student := student
		group.Add(1)
		go func() {
			defer group.Done()
			_, redeemErr := store.RedeemLibraryCDK(ctx, "hash-concurrent", student, "", "", now)
			results <- redeemErr
		}()
	}
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for redeemErr := range results {
		if redeemErr == nil {
			successes++
		} else if redeemErr == ErrLibraryCDKBound {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent redemption error: %v", redeemErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent redemption results = successes %d, conflicts %d", successes, conflicts)
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
	if err := store.CreateQuestionBank(ctx, QuestionBank{ID: "QB-SEARCH", Name: "搜索题库", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateLibraryCDKs(ctx, []LibraryCDK{{ID: "cdk-search-1", CodeHash: "search-hash-1", QuestionBankID: "QB-SEARCH", Status: "active", CreatedAt: now}, {ID: "cdk-search-2", CodeHash: "search-hash-2", QuestionBankID: "QB-SEARCH", Status: "used", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	items, total, err := store.ListLibraryCDKs(ctx, 50, 0, "QB-SEARCH")
	if err != nil {
		t.Fatalf("search query failed: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("search result = %d/%d, want 2/2", total, len(items))
	}
}
