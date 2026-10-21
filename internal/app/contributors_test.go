package app

import (
	"context"
	"testing"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

func TestBindPublishedContentContributor(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}

	// Two students who have logged into the app before.
	students := []struct {
		userID    string
		studentID string
		alias     string
	}{
		{"usr-c1", "2023000002", "张三"},
		{"usr-c2", "2023000003", "李四"},
	}
	for _, student := range students {
		if err := store.CreateUser(ctx, sqlite.User{
			ID: student.userID, Status: "active", CreatedAt: now, LastLoginAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		ciphertext, err := controlcrypto.Encrypt(a.Cfg.EncryptionKey, student.studentID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.ExecContext(ctx, `INSERT INTO identities(
				id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"idn-"+student.userID, student.userID, identityProvider,
			controlcrypto.HMACHex(a.Cfg.IDPepper, student.studentID),
			student.alias, ciphertext, now.UnixMilli(), now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}

	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材甲", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷甲", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}

	view, err := a.BindTextbookContributor(ctx, textbook.ID, "2023000002", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if view.ContributorStudentID != "2023000002" || view.ContributorName != "张三" {
		t.Fatalf("textbook contributor view = %#v", view)
	}
	if got := contributionCount(t, store, "usr-c1"); got != 1 {
		t.Fatalf("usr-c1 contributions = %d, want 1", got)
	}

	resourceView, err := a.BindResourceContributor(ctx, resource.ID, "2023000003", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if resourceView.ContributorStudentID != "2023000003" || resourceView.ContributorName != "李四" {
		t.Fatalf("resource contributor view = %#v", resourceView)
	}
	if got := contributionCount(t, store, "usr-c2"); got != 1 {
		t.Fatalf("usr-c2 contributions = %d, want 1", got)
	}

	// Moving a contribution from one student to another keeps the total stable.
	if _, err := a.BindTextbookContributor(ctx, textbook.ID, "2023000003", "admin"); err != nil {
		t.Fatal(err)
	}
	if got := contributionCount(t, store, "usr-c1"); got != 0 {
		t.Fatalf("usr-c1 contributions after transfer = %d, want 0", got)
	}
	if got := contributionCount(t, store, "usr-c2"); got != 2 {
		t.Fatalf("usr-c2 contributions after transfer = %d, want 2", got)
	}

	// Leaving the student ID empty unbinds and removes the credit.
	if _, err := a.BindTextbookContributor(ctx, textbook.ID, "", "admin"); err != nil {
		t.Fatal(err)
	}
	if got := contributionCount(t, store, "usr-c2"); got != 1 {
		t.Fatalf("usr-c2 contributions after unbind = %d, want 1", got)
	}

	if _, err := a.BindTextbookContributor(ctx, textbook.ID, "9999999999", "admin"); err == nil {
		t.Fatal("unknown student id should be rejected")
	}

	// Admin lists expose the contributor; app-facing rows must not leak it.
	items, _, err := a.ListTextbooksForAdmin(ctx, 50, 0, "", "", "")
	if err != nil || len(items) != 1 || items[0].ContributorStudentID != "" {
		// The textbook was just unbound, so the admin row has no student ID.
		t.Fatalf("admin textbooks after unbind = %#v, err = %v", items, err)
	}
	if _, err := a.BindTextbookContributor(ctx, textbook.ID, "2023000002", "admin"); err != nil {
		t.Fatal(err)
	}
	items, _, err = a.ListTextbooksForAdmin(ctx, 50, 0, "", "", "")
	if err != nil || len(items) != 1 || items[0].ContributorStudentID != "2023000002" {
		t.Fatalf("admin textbooks = %#v, err = %v", items, err)
	}
	uploadItems, err := a.ListTextbooksForUpload(ctx)
	if err != nil || len(uploadItems) != 1 || uploadItems[0].ContributorStudentID != "" {
		t.Fatalf("upload textbooks = %#v, err = %v", uploadItems, err)
	}
}

func contributionCount(t *testing.T, store *sqlite.Store, userID string) int64 {
	t.Helper()
	contribution, _, err := store.GetUserStats(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return contribution
}
