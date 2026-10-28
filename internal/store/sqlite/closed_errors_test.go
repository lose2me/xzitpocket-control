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

// TestLibraryListingsAndRedemption covers the query variants and the CDK
// redemption state machine that the console and app rely on.
func TestLibraryListingsAndRedemption(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/library.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "题库一", Status: "active", CourseCodes: []string{"C1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "题库二", Status: "disabled", CourseCode: "C2", CourseCodes: []string{"C2"}}); err != nil {
		t.Fatal(err)
	}
	archived, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "题库三", Status: "active", CourseCodes: []string{"C3"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetQuestionBankArchive(ctx, archived.ID, now); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		status   string
		archived bool
		sort     string
		order    string
		want     int
	}{
		{"", false, "updated_at", "desc", 2},
		{"active", false, "name", "asc", 1},
		{"disabled", false, "created_at", "asc", 1},
		{"active", true, "bogus", "bogus", 1},
	} {
		items, err := store.ListQuestionBanks(ctx, 10, 0, tc.status, tc.archived, tc.sort, tc.order)
		if err != nil || len(items) != tc.want {
			t.Fatalf("ListQuestionBanks(%q,%v) = %d %v, want %d", tc.status, tc.archived, len(items), err, tc.want)
		}
	}
	if count, err := store.CountQuestionBanks(ctx, "active", false); err != nil || count != 1 {
		t.Fatalf("count = %d %v", count, err)
	}
	if items, total, err := store.ListAccessibleQuestionBanks(ctx, 10, 0); err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("accessible = %v %d %v", items, total, err)
	}
	if items, err := store.ListInactiveQuestionBanks(ctx); err != nil || len(items) != 2 {
		t.Fatalf("inactive = %v %v", items, err)
	}

	// CDKs can be listed and searched and move through the status machine.
	cdks := []LibraryCDK{
		{ID: "cdk-1", QuestionBankID: first.ID, CodeHash: "hash-1", Status: "active", CreatedAt: now},
		{ID: "cdk-2", QuestionBankID: first.ID, CodeHash: "hash-2", Status: "active", CreatedAt: now},
	}
	if err := store.CreateLibraryCDKs(ctx, cdks); err != nil {
		t.Fatal(err)
	}
	for _, sort := range []string{"created_at", "status", "question_bank_id", "bogus"} {
		items, total, err := store.ListLibraryCDKs(ctx, 10, 0, sort, "desc")
		if err != nil || total != 2 || len(items) != 2 {
			t.Fatalf("ListLibraryCDKs(%q) = %v %d %v", sort, items, total, err)
		}
	}
	items, total, err := store.ListLibraryCDKs(ctx, 10, 0, "created_at", "desc", "zzz-no-match")
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("search CDKs = %v %d %v", items, total, err)
	}
	if items, total, err := store.ListLibraryCDKs(ctx, 1, 1, "created_at", "asc", "cdk"); err != nil || total < 1 || len(items) != 1 {
		t.Fatalf("paged search CDKs = %v %d %v", items, total, err)
	}
	if items, _, err := store.ListLibraryCDKs(ctx, 0, -5, "created_at", "asc"); err != nil || len(items) != 2 {
		t.Fatalf("default page CDKs = %v %v", items, err)
	}
	if err := store.SetLibraryCDKStatus(ctx, "cdk-1", "disabled"); err != nil {
		t.Fatal(err)
	}
	all, _, err := store.ListLibraryCDKs(ctx, 10, 0, "created_at", "asc")
	if err != nil || len(all) != 2 || all[0].Status != "disabled" {
		t.Fatalf("cdk list = %+v %v", all, err)
	}
	if _, err := store.RedeemLibraryCDK(ctx, "hash-1", first.ID, "student", "cipher", "", now); err == nil {
		t.Fatal("disabled CDK should not redeem")
	}
	// A different bank does not match the CDK.
	if _, err := store.RedeemLibraryCDK(ctx, "hash-2", "other-bank", "student", "cipher", "", now); err == nil {
		t.Fatal("CDK from another bank should not redeem")
	}
	redeemed, err := store.RedeemLibraryCDK(ctx, "hash-2", first.ID, "student-hash", "cipher", "", now)
	if err != nil || redeemed.Status != "used" {
		t.Fatalf("redeem = %+v %v", redeemed, err)
	}
	if again, err := store.RedeemLibraryCDK(ctx, "hash-2", first.ID, "student-hash", "cipher", "", now); err != nil || again.Status != "used" {
		t.Fatalf("same student redemption should be idempotent: %+v %v", again, err)
	}
	if _, err := store.RedeemLibraryCDK(ctx, "hash-2", first.ID, "other-student", "cipher", "", now); err == nil {
		t.Fatal("a different student must not reuse the CDK")
	}
	if ok, err := store.HasLibraryAccess(ctx, first.ID, "student-hash"); err != nil || !ok {
		t.Fatalf("has access = %v %v", ok, err)
	}
	if ok, err := store.HasLibraryAccess(ctx, first.ID, "other-student"); err != nil || ok {
		t.Fatalf("no access = %v %v", ok, err)
	}
}

// TestTelemetryAggregates covers the dashboard queries with real events.
func TestTelemetryAggregates(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/telemetry.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateUser(ctx, User{ID: "usr-1", Status: "active", CollegeName: "计算机学院", ClassName: "计科2301", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDevice(ctx, Device{ID: "dev-1", DeviceSerial: "s", Installation: "i", TokenHash: "h", PublicKey: "k", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	events := make([]EventInput, 0, 6)
	for i := 0; i < 3; i++ {
		events = append(events, EventInput{
			EventID: "evt-" + string(rune('a'+i)), Type: "app_start", UserID: "usr-1", DeviceID: "dev-1",
			OccurredAt: now.Add(-time.Duration(i) * time.Hour), Properties: `{"platform":"android","app_version":"2.0.0"}`,
		})
	}
	events = append(events, EventInput{EventID: "evt-dup", Type: "app_start", UserID: "usr-1", DeviceID: "dev-1", OccurredAt: now})
	events = append(events, EventInput{EventID: "evt-login", Type: "control_login_success", UserID: "usr-1", DeviceID: "dev-1", OccurredAt: now})
	events = append(events, EventInput{EventID: "evt-feature", Type: "campus_card", UserID: "usr-1", DeviceID: "dev-1", OccurredAt: now})
	accepted, duplicates, err := store.InsertEvents(ctx, events)
	if err != nil || accepted != len(events) || duplicates != 0 {
		t.Fatalf("insert = %d %d %v", accepted, duplicates, err)
	}
	// Replaying an event id is a duplicate, not an error.
	if accepted, duplicates, err := store.InsertEvents(ctx, events[:1]); err != nil || accepted != 0 || duplicates != 1 {
		t.Fatalf("replay = %d %d %v", accepted, duplicates, err)
	}
	overview, err := store.MetricsOverview(ctx, now)
	if err != nil || overview.TotalUsers != 1 || overview.TodayEvents == 0 {
		t.Fatalf("overview = %+v %v", overview, err)
	}
	breakdown, err := store.MetricsBreakdown(ctx, now)
	if err != nil || breakdown.Classes["计科2301"] == 0 || breakdown.EventTypes["app_start"] == 0 {
		t.Fatalf("breakdown = %+v %v", breakdown, err)
	}
	series, err := store.MetricsSeries(ctx, 7, now)
	if err != nil || len(series) == 0 {
		t.Fatalf("series = %+v %v", series, err)
	}
	if removed, err := store.CleanupEvents(ctx, now.Add(24*time.Hour)); err != nil || removed == 0 {
		t.Fatalf("cleanup = %d %v", removed, err)
	}
	// An empty database still produces zeroed aggregates.
	empty, err := Open(t.TempDir() + "/empty.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { empty.Close() })
	if err := empty.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := empty.MetricsOverview(ctx, now); err != nil {
		t.Fatalf("empty overview = %v", err)
	}
	if _, err := empty.MetricsBreakdown(ctx, now); err != nil {
		t.Fatalf("empty breakdown = %v", err)
	}
	if series, err := empty.MetricsSeries(ctx, 30, now); err != nil || len(series) == 0 {
		t.Fatalf("empty series = %+v %v", series, err)
	}
}

func TestQuestionBankContentUpdates(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/banks.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	question := func(number int, text string) Question {
		return Question{
			ID: "q" + string(rune('0'+number)), QuestionNumber: number, Type: "单选题", Title: "第" + string(rune('0'+number)) + "题",
			QuestionText: text, CorrectAnswer: "A", SortOrder: number - 1,
			Options: []QuestionOption{{ID: "o" + string(rune('0'+number)), Label: "A", Text: "对", SortOrder: 0}},
		}
	}
	bank := QuestionBank{ID: "bank-known", Name: "题库", Status: "active", CourseCodes: []string{"C1"}, CreatedAt: now, UpdatedAt: now,
		Questions: []Question{question(1, "1+1")}}
	if err := store.CreateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateQuestionBank(ctx, bank); err == nil {
		t.Fatal("duplicate explicit ID should fail")
	}
	// Updating only metadata keeps the questions.
	bank.Name = "题库改名"
	if err := store.UpdateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetQuestionBank(ctx, bank.ID)
	if err != nil || stored.Name != "题库改名" || len(stored.Questions) != 1 {
		t.Fatalf("metadata update = %+v %v", stored, err)
	}
	// Changing content replaces the question rows.
	bank.Questions = []Question{question(1, "2+2"), question(2, "3+3")}
	bank.CourseCodes = []string{"C2", "C3"}
	if err := store.UpdateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetQuestionBank(ctx, bank.ID)
	if err != nil || len(stored.Questions) != 2 || len(stored.CourseCodes) != 2 {
		t.Fatalf("content update = %+v %v", stored, err)
	}
	if err := store.UpdateQuestionBank(ctx, QuestionBank{ID: "missing", Name: "x", Status: "active"}); err == nil {
		t.Fatal("updating a missing bank should fail")
	}
	if err := store.SetQuestionBankStatus(ctx, "missing", "active", now); err == nil {
		t.Fatal("status update on a missing bank should fail")
	}
	if err := store.SetQuestionBankArchive(ctx, "missing", now); err == nil {
		t.Fatal("archive on a missing bank should fail")
	}
	if err := store.DeleteQuestionBank(ctx, bank.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetQuestionBank(ctx, bank.ID); err == nil {
		t.Fatal("deleted bank should be gone")
	}
}

func TestQuestionBankCourseReassignment(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/reassign.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	question := Question{ID: "q1", QuestionNumber: 1, Type: "单选题", Title: "第1题", QuestionText: "1+1", CorrectAnswer: "A", SortOrder: 0,
		Options: []QuestionOption{{ID: "o1", Label: "A", Text: "2", SortOrder: 0}}}
	bank := QuestionBank{ID: "bank-r", Name: "题库", Status: "active", CourseCodes: []string{"C1"}, CreatedAt: now, UpdatedAt: now, Questions: []Question{question}}
	if err := store.CreateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	// Same questions, different courses: only the association rows change.
	bank.CourseCodes = []string{"C2", "C3"}
	if err := store.UpdateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetQuestionBank(ctx, bank.ID)
	if err != nil || len(stored.CourseCodes) != 2 {
		t.Fatalf("reassigned codes = %+v %v", stored, err)
	}
	// Listing with an offset and no filter still works.
	if items, err := store.ListQuestionBanks(ctx, 1, 1, "", false, "", ""); err != nil {
		t.Fatalf("offset listing = %v", err)
	} else if len(items) != 0 {
		t.Fatalf("offset listing = %+v", items)
	}
	// Unknown CDK and invalid status are rejected.
	if _, err := store.RedeemLibraryCDK(ctx, "missing-hash", bank.ID, "student", "cipher", "", now); err == nil {
		t.Fatal("unknown CDK should fail")
	}
	if err := store.SetLibraryCDKStatus(ctx, "missing", "bogus"); err == nil {
		t.Fatal("invalid CDK status should fail")
	}
	if err := store.SetLibraryCDKStatus(ctx, "missing", "active"); err == nil {
		t.Fatal("missing CDK status should fail")
	}
}

func TestLibraryCDKSingleAndUserCounters(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/single.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateUser(ctx, User{ID: "usr-c", Status: "active", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateLibraryCDK(ctx, LibraryCDK{ID: "single", CodeHash: "single-hash", Status: "active", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	bank, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "题库", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	// An unbound CDK can be claimed by a student for any bank.
	claimed, err := store.RedeemLibraryCDK(ctx, "single-hash", bank.ID, "student", "cipher", "usr-c", now)
	if err != nil || claimed.Status != "used" || claimed.QuestionBankID != bank.ID {
		t.Fatalf("single cdk = %+v %v", claimed, err)
	}
	if err := store.CreateLibraryCDK(ctx, LibraryCDK{ID: "single-2", CodeHash: "single-hash-2", Status: "active", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLibraryCDKStatus(ctx, "single-2", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLibraryCDKStatus(ctx, "single-2", "bogus"); err == nil {
		t.Fatal("invalid cdk status should fail")
	}
	// Contribution counters are monotonic.
	if err := store.IncrementUserContribution(ctx, "usr-c"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdjustUserContribution(ctx, "usr-c", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.AdjustUserDownloads(ctx, "usr-c", 3); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementUserDownloads(ctx, "usr-c"); err != nil {
		t.Fatal(err)
	}
	contribution, downloads, err := store.GetUserStats(ctx, "usr-c")
	if err != nil || contribution != 3 || downloads != 4 {
		t.Fatalf("user stats = %d %d %v", contribution, downloads, err)
	}
	// Unknown users only report no rows.
	if err := store.IncrementUserContribution(ctx, "missing"); err == nil {
		t.Fatal("missing user contribution should fail")
	}
	if err := store.AdjustUserDownloads(ctx, "missing", 1); err == nil {
		t.Fatal("missing user download adjust should fail")
	}
	if err := store.AdjustUserContribution(ctx, "missing", 1); err == nil {
		t.Fatal("missing user contribution adjust should fail")
	}
	if err := store.IncrementUserDownloads(ctx, "missing"); err == nil {
		t.Fatal("missing user download increment should fail")
	}
}
