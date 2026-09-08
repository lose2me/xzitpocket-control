package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestErrorReportInsertIsIdempotentAndGroupedByStudentHash(t *testing.T) {
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
	if err := store.CreateUser(ctx, User{ID: "usr-errors", Status: "active", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDevice(ctx, Device{ID: "dev-errors", DeviceSerial: "serial-errors", Installation: "install-errors", TokenHash: "token", PublicKey: "key", Platform: "android", AppVersion: "2.0.0", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	input := ErrorReportInput{EventID: "error-1", UserID: "usr-errors", DeviceID: "dev-errors", StudentIDHash: "student-hash", AppVersion: "2.0.0", Platform: "android", Title: "登录失败", Message: "测试错误", OccurredAt: now, ReceivedAt: now}
	accepted, err := store.InsertErrorReport(ctx, input)
	if err != nil || !accepted {
		t.Fatalf("first error report = %v, %v", accepted, err)
	}
	accepted, err = store.InsertErrorReport(ctx, input)
	if err != nil || accepted {
		t.Fatalf("duplicate error report = %v, %v", accepted, err)
	}
	items, total, err := store.ListErrorReports(ctx, 50, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].StudentIDHash != "student-hash" {
		t.Fatalf("unexpected error reports = %d %#v (%v)", total, items, err)
	}
	if err := store.SetErrorReportStudentIgnoredByReportID(ctx, items[0].ID, true, now); err != nil {
		t.Fatal(err)
	}
	items, total, err = store.ListErrorReports(ctx, 50, 0)
	if err != nil || total != 1 || !items[0].Ignored {
		t.Fatalf("ignored report state = %d %#v (%v)", total, items, err)
	}
	input.EventID = "error-ignored"
	accepted, err = store.InsertErrorReport(ctx, input)
	if err != nil || accepted {
		t.Fatalf("ignored student report = %v, %v", accepted, err)
	}
	if err := store.SetErrorReportStudentIgnoredByReportID(ctx, items[0].ID, false, now); err != nil {
		t.Fatal(err)
	}
	accepted, err = store.InsertErrorReport(ctx, input)
	if err != nil || !accepted {
		t.Fatalf("allowed student report = %v, %v", accepted, err)
	}
	items, total, err = store.ListErrorReports(ctx, 50, 0)
	if err != nil || total != 2 || items[0].Ignored || items[1].Ignored {
		t.Fatalf("allowed report state = %d %#v (%v)", total, items, err)
	}
	if err := store.SetErrorReportStudentIgnoredByReportID(ctx, items[0].ID, true, now); err != nil {
		t.Fatal(err)
	}
	cleared, err := store.ClearErrorReports(ctx)
	if err != nil || cleared != 2 {
		t.Fatalf("clear error reports = %d, %v", cleared, err)
	}
	items, total, err = store.ListErrorReports(ctx, 50, 0)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("cleared reports = %d %#v (%v)", total, items, err)
	}
	input.EventID = "error-after-clear"
	accepted, err = store.InsertErrorReport(ctx, input)
	if err != nil || !accepted {
		t.Fatalf("report after clear = %v, %v", accepted, err)
	}
}
