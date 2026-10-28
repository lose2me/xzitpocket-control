package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestClosedStoreErrors exercises the error path of every store operation: a
// closed database must surface the driver error instead of swallowing it.
func TestClosedStoreErrors(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/closed.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := store.InitSchema(ctx); err == nil {
		t.Error("InitSchema on a closed store should fail")
	}

	expectErr := func(name string, err error) {
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%s returned %v, want a driver error", name, err)
		}
	}
	now := time.Now().UTC()

	_, err = store.ListQuestionBanks(ctx, 10, 0, "", false, "updated_at", "desc")
	expectErr("ListQuestionBanks", err)
	_, err = store.CountQuestionBanks(ctx, "", false)
	expectErr("CountQuestionBanks", err)
	_, _, err = store.ListAccessibleQuestionBanks(ctx, 10, 0)
	expectErr("ListAccessibleQuestionBanks", err)
	_, err = store.ListInactiveQuestionBanks(ctx)
	expectErr("ListInactiveQuestionBanks", err)
	_, err = store.GetQuestionBank(ctx, "id")
	expectErr("GetQuestionBank", err)
	expectErr("CreateQuestionBank", store.CreateQuestionBank(ctx, QuestionBank{ID: "b", Name: "n", Status: "active"}))
	_, err = store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "n"})
	expectErr("CreateQuestionBankAuto", err)
	expectErr("UpdateQuestionBank", store.UpdateQuestionBank(ctx, QuestionBank{ID: "b", Name: "n", Status: "active"}))
	expectErr("SetQuestionBankStatus", store.SetQuestionBankStatus(ctx, "b", "active", now))
	expectErr("SetQuestionBankArchive", store.SetQuestionBankArchive(ctx, "b", now))
	expectErr("DeleteQuestionBank", store.DeleteQuestionBank(ctx, "b"))
	_, err = store.HasLibraryAccess(ctx, "b", "hash")
	expectErr("HasLibraryAccess", err)
	expectErr("CreateLibraryCDK", store.CreateLibraryCDK(ctx, LibraryCDK{ID: "c"}))
	expectErr("CreateLibraryCDKs", store.CreateLibraryCDKs(ctx, []LibraryCDK{{ID: "c"}}))
	_, _, err = store.ListLibraryCDKs(ctx, 10, 0, "created_at", "desc")
	expectErr("ListLibraryCDKs", err)
	_, err = store.RedeemLibraryCDK(ctx, "h", "b", "s", "c", "u", now)
	expectErr("RedeemLibraryCDK", err)
	expectErr("SetLibraryCDKStatus", store.SetLibraryCDKStatus(ctx, "c", "disabled"))

	_, err = store.UploadTextbooks(ctx, []TextbookInput{{Name: "t"}}, now)
	expectErr("UploadTextbooks", err)
	_, err = store.ListTextbooks(ctx, 10, 0, "", "updated_at", "desc", false)
	expectErr("ListTextbooks", err)
	_, err = store.CountTextbooks(ctx, "", false)
	expectErr("CountTextbooks", err)
	_, err = store.ListTextbooksForCourses(ctx, []string{"C1"})
	expectErr("ListTextbooksForCourses", err)
	_, err = store.ListAllTextbooks(ctx)
	expectErr("ListAllTextbooks", err)
	_, err = store.GetTextbook(ctx, "t")
	expectErr("GetTextbook", err)
	_, err = store.CreateTextbook(ctx, TextbookInput{Name: "t"}, now)
	expectErr("CreateTextbook", err)
	expectErr("UpdateTextbook", store.UpdateTextbook(ctx, "t", TextbookInput{Name: "t"}, now))
	expectErr("DeleteTextbook", store.DeleteTextbook(ctx, "t"))
	expectErr("SetTextbookPdfKey", store.SetTextbookPdfKey(ctx, "t", "k", now))
	expectErr("IncrementTextbookDownloads", store.IncrementTextbookDownloads(ctx, "t"))
	expectErr("SetTextbookContributor", store.SetTextbookContributor(ctx, "t", "u"))
	expectErr("SetTextbookArchive", store.SetTextbookArchive(ctx, "t", "p", "c", now))
	expectErr("SetTextbookCoverKey", store.SetTextbookCoverKey(ctx, "t", "k", now))

	_, _, err = store.CreateAITask(ctx, "r", "actor")
	expectErr("CreateAITask", err)
	_, err = store.CreateAITasksForMissingResources(ctx, 10, "actor")
	expectErr("CreateAITasksForMissingResources", err)
	_, err = store.RequeueFailedAITasks(ctx, 0, 10)
	expectErr("RequeueFailedAITasks", err)
	_, err = store.GetAITask(ctx, "t")
	expectErr("GetAITask", err)
	_, _, err = store.GetActiveAITaskByResource(ctx, "r")
	expectErr("GetActiveAITaskByResource", err)
	_, err = store.GetAITaskRow(ctx, "t")
	expectErr("GetAITaskRow", err)
	_, _, err = store.ListAITasks(ctx, "", "", 10, 0)
	expectErr("ListAITasks", err)
	_, err = store.ListAITasksByResource(ctx, "r")
	expectErr("ListAITasksByResource", err)
	_, err = store.DeleteOlderAITasksForResource(ctx, "r", "keep")
	expectErr("DeleteOlderAITasksForResource", err)
	_, _, err = store.ClaimNextAITask(ctx)
	expectErr("ClaimNextAITask", err)
	status := "queued"
	expectErr("UpdateAITask", store.UpdateAITask(ctx, "t", AITaskPatch{Status: &status}))
	_, err = store.ResetRunningAITasks(ctx)
	expectErr("ResetRunningAITasks", err)
	_, err = store.LatestSucceededAITasks(ctx, []string{"r"})
	expectErr("LatestSucceededAITasks", err)
	_, _, err = store.LatestAITaskByResource(ctx, "r")
	expectErr("LatestAITaskByResource", err)
	_, err = store.LatestAITasks(ctx, []string{"r"})
	expectErr("LatestAITasks", err)

	_, err = store.CreateSubmission(ctx, SubmissionInput{}, now)
	expectErr("CreateSubmission", err)
	_, err = store.ListSubmissions(ctx, "resource", 10, 0)
	expectErr("ListSubmissions", err)
	_, err = store.CountSubmissions(ctx, "resource")
	expectErr("CountSubmissions", err)
	_, err = store.CountSubmissionsByUser(ctx, "u")
	expectErr("CountSubmissionsByUser", err)
	_, err = store.CountUploadingSubmissionsByUser(ctx, "u")
	expectErr("CountUploadingSubmissionsByUser", err)
	_, err = store.ListStaleSubmissions(ctx, now, 10)
	expectErr("ListStaleSubmissions", err)
	_, err = store.GetSubmission(ctx, "s")
	expectErr("GetSubmission", err)
	_, err = store.AddSubmissionFile(ctx, SubmissionFileInput{SubmissionID: "s"}, now)
	expectErr("AddSubmissionFile", err)
	_, err = store.GetSubmissionFile(ctx, "f")
	expectErr("GetSubmissionFile", err)
	_, err = store.ListSubmissionFiles(ctx, []string{"s"})
	expectErr("ListSubmissionFiles", err)
	expectErr("UpdateSubmissionFileSize", store.UpdateSubmissionFileSize(ctx, "f", 1))
	expectErr("DeleteSubmissionFile", store.DeleteSubmissionFile(ctx, "f"))
	_, err = store.CountSubmissionFileKeyRefs(ctx, "k")
	expectErr("CountSubmissionFileKeyRefs", err)
	_, err = store.CountSubmissionFiles(ctx, "s")
	expectErr("CountSubmissionFiles", err)
	expectErr("MarkSubmissionUploaded", store.MarkSubmissionUploaded(ctx, "s", 1, now))
	expectErr("DeleteUploadingSubmission", store.DeleteUploadingSubmission(ctx, "s"))
	expectErr("DeleteSubmission", store.DeleteSubmission(ctx, "s"))
	_, err = store.PdfKeyInUse(ctx, "k")
	expectErr("PdfKeyInUse", err)
	_, err = store.CoverKeyInUse(ctx, "k")
	expectErr("CoverKeyInUse", err)

	_, _, err = store.InsertEvents(ctx, []EventInput{{EventID: "e", Type: "app_start", OccurredAt: now}})
	expectErr("InsertEvents", err)
	_, err = store.CleanupEvents(ctx, now)
	expectErr("CleanupEvents", err)
	_, err = store.MetricsOverview(ctx, now)
	expectErr("MetricsOverview", err)
	_, err = store.MetricsBreakdown(ctx, now)
	expectErr("MetricsBreakdown", err)
	_, err = store.MetricsSeries(ctx, 30, now)
	expectErr("MetricsSeries", err)
}

// TestClosedStoreErrorsKeepsNoRowsDistinct guards that a missing row is still
// reported as sql.ErrNoRows on a working database.
func TestMissingRowsStayNoRows(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/rows.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAITask(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing AI task = %v", err)
	}
	if _, err := store.GetSubmission(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing submission = %v", err)
	}
	if err := store.DeleteQuestionBank(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing question bank delete = %v", err)
	}
	if err := store.DeleteTextbook(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing textbook delete = %v", err)
	}
}
