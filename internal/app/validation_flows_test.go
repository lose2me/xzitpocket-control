package app

import (
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func validQuestionBank(name string) QuestionBankInput {
	return QuestionBankInput{
		Name:        name,
		CourseCodes: []string{"C1"},
		Questions: []QuestionInput{{
			QuestionNumber: 1, Type: "单选题", Title: "第1题", QuestionText: "1+1=?",
			Options:       []OptionInput{{Label: "A", Text: "1"}, {Label: "B", Text: "2"}},
			CorrectAnswer: "B",
		}},
	}
}

func TestQuestionBankValidationRejectsBadInput(t *testing.T) {
	a, _, ctx := newStorageApp(t)
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C2", Name: "课程二"}, "admin"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*QuestionBankInput)
	}{
		{"empty name", func(in *QuestionBankInput) { in.Name = " " }},
		{"long name", func(in *QuestionBankInput) { in.Name = strings.Repeat("名", 300) }},
		{"newline name", func(in *QuestionBankInput) { in.Name = "题\n库" }},
		{"bad course code", func(in *QuestionBankInput) { in.CourseCodes = []string{strings.Repeat("C", 70)} }},
		{"too many courses", func(in *QuestionBankInput) {
			in.CourseCodes = make([]string, 21)
			for i := range in.CourseCodes {
				in.CourseCodes[i] = "C" + string(rune('A'+i))
			}
		}},
		{"bad status", func(in *QuestionBankInput) { in.Status = "archived" }},
		{"zero question number", func(in *QuestionBankInput) { in.Questions[0].QuestionNumber = 0 }},
		{"duplicate question number", func(in *QuestionBankInput) {
			in.Questions = append(in.Questions, in.Questions[0])
		}},
		{"bad type", func(in *QuestionBankInput) { in.Questions[0].Type = "简答题" }},
		{"empty title", func(in *QuestionBankInput) { in.Questions[0].Title = "" }},
		{"empty question text", func(in *QuestionBankInput) { in.Questions[0].QuestionText = "" }},
		{"too many options", func(in *QuestionBankInput) {
			in.Questions[0].Options = make([]OptionInput, 65)
			for i := range in.Questions[0].Options {
				in.Questions[0].Options[i] = OptionInput{Label: "L" + string(rune('A'+i)), Text: "x"}
			}
		}},
		{"bad option label", func(in *QuestionBankInput) { in.Questions[0].Options[0].Label = "A B" }},
		{"duplicate option label", func(in *QuestionBankInput) { in.Questions[0].Options[1].Label = "A" }},
		{"empty option text", func(in *QuestionBankInput) { in.Questions[0].Options[0].Text = "" }},
		{"answer not an option", func(in *QuestionBankInput) { in.Questions[0].CorrectAnswer = "C" }},
		{"multi answer with unknown option", func(in *QuestionBankInput) {
			in.Questions[0].Type = "多选题"
			in.Questions[0].CorrectAnswer = "A,C"
		}},
		{"multi answer duplicated", func(in *QuestionBankInput) {
			in.Questions[0].Type = "多选题"
			in.Questions[0].CorrectAnswer = "A,A"
		}},
		{"fill in with options", func(in *QuestionBankInput) {
			in.Questions[0].Type = "填空题"
			in.Questions[0].CorrectAnswer = "2"
		}},
		{"fill in without answer", func(in *QuestionBankInput) {
			in.Questions[0].Type = "填空题"
			in.Questions[0].Options = nil
			in.Questions[0].CorrectAnswer = " "
		}},
		{"judgement without options", func(in *QuestionBankInput) {
			in.Questions[0].Type = "判断题"
			in.Questions[0].Options = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validQuestionBank("题库")
			tc.mutate(&in)
			if _, err := a.CreateQuestionBank(ctx, in, "admin"); err == nil {
				t.Fatalf("%s should be rejected", tc.name)
			}
		})
	}

	// Multi-choice answers are normalised into option order.
	in := validQuestionBank("多选题库")
	in.Questions[0].Type = "多选题"
	in.Questions[0].Options = []OptionInput{{Label: "A", Text: "1"}, {Label: "B", Text: "2"}, {Label: "C", Text: "3"}}
	in.Questions[0].CorrectAnswer = "C, A"
	created, err := a.CreateQuestionBank(ctx, in, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if created.QuestionBank.Questions[0].CorrectAnswer != "A,C" {
		t.Fatalf("normalised answer = %q", created.QuestionBank.Questions[0].CorrectAnswer)
	}
	// Blank course codes are ignored and duplicates de-duplicated.
	if _, err := a.CreateQuestionBank(ctx, QuestionBankInput{Name: "无课程题库", CourseCodes: []string{"", "C2", "C2"}}, "admin"); err != nil {
		t.Fatalf("blank course codes should be accepted: %v", err)
	}
}

func TestQuestionBankCRUDErrors(t *testing.T) {
	a, _, ctx := newStorageApp(t)
	created, err := a.CreateQuestionBank(ctx, validQuestionBank("题库"), "admin")
	if err != nil {
		t.Fatal(err)
	}
	id := created.QuestionBank.ID

	if _, err := a.UpdateQuestionBank(ctx, id, QuestionBankInput{ID: "other", Name: "x"}, "admin"); err == nil {
		t.Fatal("id mismatch should fail")
	}
	if _, err := a.UpdateQuestionBank(ctx, "missing", validQuestionBank("题库"), "admin"); err == nil {
		t.Fatal("missing bank update should fail")
	}
	if _, err := a.UpdateQuestionBank(ctx, id, QuestionBankInput{ID: id, Name: ""}, "admin"); err == nil {
		t.Fatal("invalid update should fail")
	}
	updated, err := a.UpdateQuestionBank(ctx, id, QuestionBankInput{ID: id, Name: "题库二", Status: "disabled", CourseCodes: []string{"C2"}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if updated.QuestionBank.Name != "题库二" || updated.Status != "disabled" || updated.QuestionBank.CourseCode != "C2" {
		t.Fatalf("updated bank = %#v", updated)
	}
	if err := a.SetQuestionBankStatus(ctx, id, "bogus", "admin"); err == nil {
		t.Fatal("invalid status should fail")
	}
	if err := a.SetQuestionBankStatus(ctx, "missing", "active", "admin"); err == nil {
		t.Fatal("missing status update should fail")
	}
	if err := a.SetQuestionBankStatus(ctx, id, "active", "admin"); err != nil {
		t.Fatal(err)
	}
	// Archiving is idempotent and restore flips it back.
	archived, err := a.SetQuestionBankArchived(ctx, id, true, "admin")
	if err != nil || !archived.Archived {
		t.Fatalf("archive = %#v %v", archived, err)
	}
	again, err := a.SetQuestionBankArchived(ctx, id, true, "admin")
	if err != nil || !again.Archived {
		t.Fatalf("repeat archive = %#v %v", again, err)
	}
	restored, err := a.SetQuestionBankArchived(ctx, id, false, "admin")
	if err != nil || restored.Archived {
		t.Fatalf("restore = %#v %v", restored, err)
	}
	if _, err := a.SetQuestionBankArchived(ctx, "missing", true, "admin"); err == nil {
		t.Fatal("missing archive should fail")
	}
	if _, err := a.GetAdminQuestionBank(ctx, "missing"); err == nil {
		t.Fatal("missing admin bank should fail")
	}
	if _, err := a.GetQuestionBank(ctx, "missing"); err == nil {
		t.Fatal("missing public bank should fail")
	}
	if _, err := a.GetQuestionBank(ctx, ""); err == nil {
		t.Fatal("blank bank id should fail")
	}
	if _, _, err := a.ListQuestionBanks(ctx, 10, 0, "", false, "bogus-sort", "bogus-order"); err != nil {
		t.Fatalf("list with unknown sort should not fail: %v", err)
	}

	// A CDK bound to the bank blocks deletion.
	cdks, err := a.CreateLibraryCDKs(ctx, LibraryCDKCreateInput{Count: 1}, "admin")
	if err != nil || len(cdks) != 1 {
		t.Fatalf("cdk = %v %v", cdks, err)
	}
	if err := a.DeleteQuestionBank(ctx, id, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteQuestionBank(ctx, id, "admin"); err == nil {
		t.Fatal("deleting a missing bank should fail")
	}
}

func TestSchoolCalendarValidationErrors(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	valid := SchoolCalendarInput{
		Days:  []SchoolCalendarDay{{Date: "2026-09-01", Name: "开学"}, {Date: "2026-09-02", Name: "", Adjustment: "20260905"}},
		Slots: []SchoolCalendarSlot{{Index: 1, Start: "08:00", End: "08:45"}},
	}
	if _, err := a.UpdateSchoolCalendar(ctx, SchoolCalendarInput{}, "admin"); err == nil {
		t.Fatal("empty calendar should fail")
	}
	bad := []SchoolCalendarInput{
		{Days: []SchoolCalendarDay{{Date: "2026-13-01"}}},
		{Days: []SchoolCalendarDay{{Date: "2026-09-01"}, {Date: "2026-09-01"}}},
		{Days: []SchoolCalendarDay{{Date: "2026-09-01", Name: strings.Repeat("名", 70)}}},
		{Days: []SchoolCalendarDay{{Date: "2026-09-01", Adjustment: "2026-09-05"}}},
		{Days: []SchoolCalendarDay{{Date: "2026-09-01"}, {Date: "2026-09-30"}}},
		{Days: valid.Days, Slots: []SchoolCalendarSlot{{Index: 1, Start: "8:00", End: "08:45"}}},
		{Days: valid.Days, Slots: []SchoolCalendarSlot{{Index: 1, Start: "08:45", End: "08:00"}}},
		{Days: valid.Days, Slots: []SchoolCalendarSlot{{Index: 1, Start: "08:00", End: "08:00"}}},
		{Days: valid.Days, Slots: make([]SchoolCalendarSlot, 31)},
	}
	for _, in := range bad {
		if _, err := a.UpdateSchoolCalendar(ctx, in, "admin"); err == nil {
			t.Fatalf("calendar %#v should be rejected", in)
		}
	}
	if _, err := a.UpdateSchoolCalendar(ctx, valid, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSchoolCalendarExceptions(ctx, SchoolCalendarExceptionsInput{Exceptions: []SchoolCalendarDay{{Date: "2026-10-01"}}}, "admin"); err == nil {
		t.Fatal("exception outside the range should fail")
	}
	if _, err := a.SaveSchoolCalendarExceptions(ctx, SchoolCalendarExceptionsInput{Exceptions: []SchoolCalendarDay{{Date: "2026-09-01"}, {Date: "2026-09-01"}}}, "admin"); err == nil {
		t.Fatal("duplicate exceptions should fail")
	}
	if _, err := a.SaveSchoolCalendarExceptions(ctx, SchoolCalendarExceptionsInput{College: "计算机学院", Exceptions: []SchoolCalendarDay{{Date: "2026-09-02", Name: "院庆"}}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetSchoolCalendarForCollege(ctx, "计算机学院"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteSchoolCalendarExceptions(ctx, "", "admin"); err == nil {
		t.Fatal("deleting global exceptions should fail")
	}
	if _, err := a.DeleteSchoolCalendarExceptions(ctx, "计算机学院", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteSchoolCalendarExceptions(ctx, "机械学院", "admin"); err != nil {
		t.Fatalf("deleting an unknown override is idempotent: %v", err)
	}
	// Corrupted stored JSON falls back to an empty calendar instead of failing.
	if _, err := store.DB.ExecContext(ctx, "UPDATE school_calendar_config SET days_json = 'not-json', slots_json = 'not-json' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	out, err := a.GetSchoolCalendarAdmin(ctx)
	if err != nil || len(out.Days) == 0 {
		t.Fatalf("corrupted calendar should be reset to defaults: %d days, %v", len(out.Days), err)
	}
}

func TestCourseAndAdminMaintenanceErrors(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	sqliteStore := store
	now := time.Now().UTC()

	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "", Name: "x"}, "admin"); err == nil {
		t.Fatal("course without code should fail")
	}
	if _, err := a.CreateCourseForAdmin(ctx, CourseWriteInput{Code: "C1", Name: "重复"}, "admin"); err == nil {
		t.Fatal("duplicate course should fail")
	}
	if _, err := a.UpdateCourseForAdmin(ctx, "missing", CourseWriteInput{Code: "C9", Name: "x"}, "admin"); err == nil {
		t.Fatal("updating a missing course should fail")
	}
	if _, _, err := a.ListCoursesForAdmin(ctx, 10, 0, "", "bogus", "bogus"); err != nil {
		t.Fatalf("unknown sort should fall back: %v", err)
	}
	resource, err := sqliteStore.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteCourseForAdmin(ctx, "C1", "admin"); err == nil {
		t.Fatal("deleting a course with content should fail")
	}
	if err := sqliteStore.DeleteResource(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "Bad Name", Key: "long-enough-key", Permissions: "users=r"}, "admin"); err == nil {
		t.Fatal("invalid admin name should fail")
	}
	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "short", Key: "short", Permissions: "users=r"}, "admin"); err == nil {
		t.Fatal("short admin key should fail")
	}
	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "badperm", Key: "long-enough-key", Permissions: "users=x"}, "admin"); err == nil {
		t.Fatal("invalid permissions should fail")
	}
	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "wild", Key: "long-enough-key", Permissions: "*=rw"}, "admin"); err == nil {
		t.Fatal("wildcard grant should fail")
	}
	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "admin", Key: "long-enough-key", Permissions: "users=r"}, "admin"); err == nil {
		t.Fatal("recreating the super admin should fail")
	}
	created, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "helper", Key: "long-enough-key", Permissions: "users=r"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateAdminForAdmin(ctx, "missing", AdminWriteInput{Name: "helper2", Permissions: "users=rw"}, "admin"); err == nil {
		t.Fatal("updating a missing admin should fail")
	}
	// Keep the same key when the update omits it.
	if _, err := a.UpdateAdminForAdmin(ctx, created.ID, AdminWriteInput{Name: "helper2", Permissions: "users=rw"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateAdminForAdmin(ctx, created.ID, AdminWriteInput{Name: "helper2", Key: "short", Permissions: "users=rw"}, "admin"); err == nil {
		t.Fatal("short replacement key should fail")
	}
	if err := a.DeleteAdminForAdmin(ctx, "missing", "admin"); err == nil {
		t.Fatal("deleting a missing admin should fail")
	}
	if err := a.DeleteAdminForAdmin(ctx, created.ID, "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityHelpersSurviveClosedStore(t *testing.T) {
	a, store, ctx := newStorageApp(t)
	now := time.Now().UTC()
	device := sqlite.Device{ID: "dev-1", DeviceSerial: "serial-1", Installation: "inst-1", TokenHash: "hash", PublicKey: "key", Platform: "android", AppVersion: "1", CreatedAt: now, LastSeenAt: now}
	if err := store.CreateDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, sqlite.User{ID: "usr-1", Status: "active", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	principal := DevicePrincipal{Device: device, Token: "token"}
	if _, err := a.CreateChallenge(ctx, principal); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := a.RegisterDevice(ctx, RegisterDeviceInput{InstallationID: "inst-2", PublicKey: "k", Platform: "android", CreatedAt: now.Format(time.RFC3339)}, "sig"); err == nil {
		t.Fatal("registration with a closed store should fail")
	}
	if _, err := a.CreateChallenge(ctx, principal); err == nil {
		t.Fatal("challenge with a closed store should fail")
	}
	if err := a.CheckLoginEligibility(ctx, "20230001"); err == nil {
		t.Fatal("eligibility with a closed store should fail")
	}
	if _, err := a.RefreshSession(ctx, "token", "sig", device.Installation, now.Format(time.RFC3339), "20230001"); err == nil {
		t.Fatal("refresh with a closed store should fail")
	}
	if _, err := a.CurrentUser(ctx, SessionPrincipal{User: sqlite.User{ID: "usr-1"}}); err == nil {
		t.Fatal("current user with a closed store should fail")
	}
	if _, err := a.UserDevices(ctx, SessionPrincipal{User: sqlite.User{ID: "usr-1"}}); err == nil {
		t.Fatal("user devices with a closed store should fail")
	}
	if err := a.RevokeSession(ctx, SessionPrincipal{Session: sqlite.Session{ID: "ses-1"}}); err == nil {
		t.Fatal("revoke with a closed store should fail")
	}
	if err := a.ValidateReportingStudent(ctx, "usr-1", "20230001"); err == nil {
		t.Fatal("student validation with a closed store should fail")
	}
	if _, err := a.GetUploadStats(ctx, SessionPrincipal{User: sqlite.User{ID: "usr-1"}}); err == nil {
		t.Fatal("upload stats with a closed store should fail")
	}
	if _, err := a.ListResourceCourses(ctx); err == nil {
		t.Fatal("resource courses with a closed store should fail")
	}
	if _, _, _, err := a.ListCourseResourcesForUser(ctx, "C1"); err == nil {
		t.Fatal("course resources with a closed store should fail")
	}
	if _, _, _, err := a.ListCourseTextbooksForUser(ctx, "C1"); err == nil {
		t.Fatal("course textbooks with a closed store should fail")
	}
}
