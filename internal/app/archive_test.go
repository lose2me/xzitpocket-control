package app

import (
	"context"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func seedStatsUser(t *testing.T, store *sqlite.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateUser(context.Background(), sqlite.User{
		ID: id, Status: "active", CreatedAt: now, LastLoginAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveTextbookKeepsAttribution(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a.cosClientOverride = newStubCOSClient(nil, nil)
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	seedStatsUser(t, store, "usr-archive-book")
	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材甲", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef0123456789abcdef"
	if err := store.SetTextbookPdfKey(ctx, textbook.ID, publishedObjectKey("textbook", fileFormatPDF, hash), now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookCoverKey(ctx, textbook.ID, textbookCoverKey(hash), now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookContributor(ctx, textbook.ID, "usr-archive-book"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := store.IncrementTextbookDownloads(ctx, textbook.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AdjustUserContribution(ctx, "usr-archive-book", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.AdjustUserDownloads(ctx, "usr-archive-book", 3); err != nil {
		t.Fatal(err)
	}

	archived, err := a.SetTextbookArchived(ctx, textbook.ID, true, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived {
		t.Fatal("textbook should be archived")
	}
	stored, err := store.GetTextbook(ctx, textbook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PdfKey != archivedObjectKey("textbook", fileFormatPDF, hash) ||
		stored.CoverKey != archivedTextbookCoverKey(hash) ||
		stored.ArchivedAt.IsZero() {
		t.Fatalf("archived textbook = %#v", stored)
	}
	assertUserStats(t, store, "usr-archive-book", 1, 3)

	active, total, err := a.ListTextbooksForAdmin(ctx, 50, 0, "", "", "", false)
	if err != nil || total != 0 || len(active) != 0 {
		t.Fatalf("active textbooks = %#v, total = %d, err = %v", active, total, err)
	}
	archivedList, total, err := a.ListTextbooksForAdmin(ctx, 50, 0, "", "", "", true)
	if err != nil || total != 1 || len(archivedList) != 1 {
		t.Fatalf("archived textbooks = %#v, total = %d, err = %v", archivedList, total, err)
	}
	courseBooks, _, _, err := a.ListCourseTextbooksForUser(ctx, "C1")
	if err != nil || len(courseBooks) != 0 {
		t.Fatalf("app textbooks = %#v, err = %v", courseBooks, err)
	}
	if _, err := a.TextbookResourceURL(ctx, textbook.ID, "pdf", 0); err == nil {
		t.Fatal("archived textbook URL should not be served to the app")
	}

	restored, err := a.SetTextbookArchived(ctx, textbook.ID, false, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Archived {
		t.Fatal("textbook should be restored")
	}
	stored, err = store.GetTextbook(ctx, textbook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PdfKey != publishedObjectKey("textbook", fileFormatPDF, hash) ||
		stored.CoverKey != textbookCoverKey(hash) ||
		!stored.ArchivedAt.IsZero() {
		t.Fatalf("restored textbook = %#v", stored)
	}
	assertUserStats(t, store, "usr-archive-book", 1, 3)
	courseBooks, _, _, err = a.ListCourseTextbooksForUser(ctx, "C1")
	if err != nil || len(courseBooks) != 1 {
		t.Fatalf("restored app textbooks = %#v, err = %v", courseBooks, err)
	}
}

func TestArchiveResourceKeepsFormatAndAttribution(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a.cosClientOverride = newStubCOSClient(nil, nil)
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "课程一"}, "admin"); err != nil {
		t.Fatal(err)
	}
	seedStatsUser(t, store, "usr-archive-paper")
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷甲", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	hash := "fedcba9876543210fedcba9876543210"
	if err := store.SetResourcePdfKey(ctx, resource.ID, publishedObjectKey("resource", fileFormatDOCX, hash), now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourceContributor(ctx, resource.ID, "usr-archive-paper"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.IncrementResourceDownloads(ctx, resource.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AdjustUserContribution(ctx, "usr-archive-paper", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.AdjustUserDownloads(ctx, "usr-archive-paper", 2); err != nil {
		t.Fatal(err)
	}

	archived, err := a.SetResourceArchived(ctx, resource.ID, true, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived || archived.Format != fileFormatDOCX {
		t.Fatalf("archived resource = %#v", archived)
	}
	stored, err := store.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PdfKey != archivedObjectKey("resource", fileFormatDOCX, hash) || stored.ArchivedAt.IsZero() {
		t.Fatalf("archived resource row = %#v", stored)
	}
	assertUserStats(t, store, "usr-archive-paper", 1, 2)
	if items, total, _, err := a.ListCourseResourcesForUser(ctx, "C1"); err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("app resources = %#v, total = %d, err = %v", items, total, err)
	}
	if _, err := a.ResourceURL(ctx, resource.ID, 0); err == nil {
		t.Fatal("archived resource URL should not be served to the app")
	}
	if items, total, err := a.ListResourcesForAdmin(ctx, 50, 0, "", "C1", "", "", true); err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("archived admin resources = %#v, total = %d, err = %v", items, total, err)
	}

	if _, err := a.SetResourceArchived(ctx, resource.ID, false, "admin"); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PdfKey != publishedObjectKey("resource", fileFormatDOCX, hash) || !stored.ArchivedAt.IsZero() {
		t.Fatalf("restored resource row = %#v", stored)
	}
	assertUserStats(t, store, "usr-archive-paper", 1, 2)
	if items, total, _, err := a.ListCourseResourcesForUser(ctx, "C1"); err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("restored app resources = %#v, total = %d, err = %v", items, total, err)
	}
}

func TestArchiveQuestionBankHidesItFromTheApp(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedStatsUser(t, store, "usr-archive-bank")
	created, err := store.CreateQuestionBankAuto(ctx, sqlite.QuestionBank{
		Name: "题库甲", Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.SetQuestionBankArchived(ctx, created.ID, true, "admin"); err != nil {
		t.Fatal(err)
	}
	if items, total, err := a.ListQuestionBanksForUser(ctx, 50, 0, "usr-archive-bank"); err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("app banks = %#v, total = %d, err = %v", items, total, err)
	}
	if _, err := a.GetQuestionBankForUser(ctx, created.ID, "usr-archive-bank"); err == nil {
		t.Fatal("archived bank should not open in the app")
	}
	hidden, err := a.ListInactiveQuestionBanks(ctx)
	if err != nil || len(hidden) != 1 || hidden[0].ID != created.ID {
		t.Fatalf("hidden banks = %#v, err = %v", hidden, err)
	}
	if items, total, err := a.ListQuestionBanks(ctx, 50, 0, "", false, "", ""); err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("active admin banks = %#v, total = %d, err = %v", items, total, err)
	}
	if items, total, err := a.ListQuestionBanks(ctx, 50, 0, "", true, "", ""); err != nil || total != 1 || len(items) != 1 || !items[0].Archived {
		t.Fatalf("archived admin banks = %#v, total = %d, err = %v", items, total, err)
	}

	if _, err := a.SetQuestionBankArchived(ctx, created.ID, false, "admin"); err != nil {
		t.Fatal(err)
	}
	if items, total, err := a.ListQuestionBanksForUser(ctx, 50, 0, "usr-archive-bank"); err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("restored app banks = %#v, total = %d, err = %v", items, total, err)
	}
	if _, err := a.GetQuestionBankForUser(ctx, created.ID, "usr-archive-bank"); err != nil {
		t.Fatalf("restored bank should open: %v", err)
	}
	if _, err := a.Store.GetQuestionBank(ctx, created.ID); err != nil {
		t.Fatalf("restored bank missing: %v", err)
	}
}
