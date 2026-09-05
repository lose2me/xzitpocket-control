package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

func TestQuestionBankValidationAndRoundTrip(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{TokenPepper: []byte("token"), IDPepper: []byte("id"), EncryptionKey: []byte("01234567890123456789012345678901"), AdminKey: "test"}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateQuestionBank(context.Background(), QuestionBankInput{ID: "QB-001", OrderID: 4, New: true, Name: "基础测验", Questions: []QuestionInput{
		{QuestionNumber: 2, Type: "多选题", Title: "第二题", QuestionText: "选择系统", Options: []OptionInput{{Label: "A", Text: "Windows"}, {Label: "B", Text: "Linux"}}, CorrectAnswer: "B,A"},
		{QuestionNumber: 1, Type: "填空题", Title: "第一题", QuestionText: "1 GB 等于 ____ MB。", CorrectAnswer: "1024"},
	}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if created.QuestionBank.OrderID != 4 || created.QuestionBank.Questions[0].QuestionNumber != 1 || created.QuestionBank.Questions[1].CorrectAnswer != "A,B" {
		t.Fatalf("unexpected normalized bank: %#v", created)
	}
	got, err := a.GetQuestionBank(context.Background(), "QB-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.QuestionBank.ID != "QB-001" || len(got.QuestionBank.Questions) != 2 || got.QuestionBank.Questions[0].Options == nil || len(got.QuestionBank.Questions[1].Options) != 2 {
		t.Fatalf("unexpected public bank: %#v", got)
	}
	if err := a.SetQuestionBankStatus(context.Background(), "QB-001", "disabled", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetQuestionBank(context.Background(), "QB-001"); err == nil {
		t.Fatal("disabled bank should not be publicly readable")
	}
}

func TestLibraryCDKUnlocksQuestionBankForStudent(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{TokenPepper: []byte("token"), IDPepper: []byte("id"), EncryptionKey: []byte("01234567890123456789012345678901"), AdminKey: "test"}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateQuestionBank(ctx, QuestionBankInput{New: true, RequiresCDK: true, Name: "受限题库", Questions: []QuestionInput{{QuestionNumber: 1, Type: "填空题", Title: "题", QuestionText: "题干", CorrectAnswer: "答案"}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetQuestionBankForUser(ctx, created.QuestionBank.ID, "missing-user"); err == nil {
		t.Fatal("protected bank should remain locked without a student identity")
	}
	second, err := a.CreateQuestionBank(ctx, QuestionBankInput{New: false, RequiresCDK: true, Name: "另一受限题库", Questions: []QuestionInput{{QuestionNumber: 1, Type: "填空题", Title: "题", QuestionText: "题干", CorrectAnswer: "答案"}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}

	student := "2023000001"
	studentHash := controlcrypto.HMACHex(a.Cfg.IDPepper, student)
	now := time.Now().UTC()
	user := sqlite.User{ID: "usr-test", Status: "active", CreatedAt: now, LastLoginAt: now}
	identity := sqlite.Identity{ID: "idn-test", UserID: user.ID, Provider: identityProvider, StudentIDHash: studentHash, StudentAlias: controlcrypto.StudentAlias(student), StudentIDCiphertext: "cipher", CreatedAt: now, UpdatedAt: now}
	if _, err := store.FindOrCreateIdentity(ctx, user, identity); err != nil {
		t.Fatal(err)
	}
	p := SessionPrincipal{User: user}
	cdk, err := a.CreateLibraryCDK(ctx, LibraryCDKCreateInput{}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RedeemLibraryCDK(ctx, p, cdk.Code, created.QuestionBank.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetQuestionBankForUser(ctx, created.QuestionBank.ID, user.ID); err != nil {
		t.Fatalf("unlocked bank should be readable: %v", err)
	}
	if _, err := a.GetQuestionBankForUser(ctx, second.QuestionBank.ID, user.ID); err == nil {
		t.Fatal("one CDK should unlock only the selected question bank")
	}
	if _, err := a.RedeemLibraryCDK(ctx, p, cdk.Code, second.QuestionBank.ID); err == nil {
		t.Fatal("a bound CDK should not be reusable for another question bank")
	}
}
