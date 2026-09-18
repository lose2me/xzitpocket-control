package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestQuestionBankOrderIDsAllocateSortAndRejectDuplicates(t *testing.T) {
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
	first, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "自动题库", Status: "active", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "QB-001" || first.OrderID != 1 {
		t.Fatalf("unexpected first bank allocation: %#v", first)
	}
	manual, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "手动题库", OrderID: 7, Status: "active", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if manual.OrderID != 7 {
		t.Fatalf("manual order was not retained: %#v", manual)
	}
	next, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "下一个题库", Status: "active", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if next.OrderID != 8 {
		t.Fatalf("automatic order did not advance past manual value: %#v", next)
	}
	_, err = store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "重复题库", OrderID: 7, Status: "active", CreatedAt: now, UpdatedAt: now})
	if !errors.Is(err, ErrQuestionBankOrderTaken) {
		t.Fatalf("duplicate order error = %v, want ErrQuestionBankOrderTaken", err)
	}
	items, err := store.ListQuestionBanks(ctx, 50, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].OrderID != 1 || items[1].OrderID != 7 || items[2].OrderID != 8 {
		t.Fatalf("banks were not sorted by order: %#v", items)
	}
	manual.OrderID = 2
	manual.UpdatedAt = now.Add(time.Minute)
	if err := store.UpdateQuestionBank(ctx, manual); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListQuestionBanks(ctx, 50, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].OrderID != 1 || items[1].OrderID != 2 || items[2].OrderID != 8 {
		t.Fatalf("updated order was not applied: %#v", items)
	}
}

func TestQuestionBankRepositoryReplacesQuestionsAtomically(t *testing.T) {
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
	bank := QuestionBank{ID: "QB-001", IsNew: true, Name: "测试", Status: "active", CreatedAt: now, UpdatedAt: now, Questions: []Question{{ID: "q-1", QuestionNumber: 1, Type: "单选题", Title: "题目", QuestionText: "题干", CorrectAnswer: "A", SortOrder: 0, Options: []QuestionOption{{ID: "o-1", Label: "A", Text: "答案", SortOrder: 0}}}}}
	if err := store.CreateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	bank.Name = "已更新"
	bank.UpdatedAt = now.Add(time.Minute)
	bank.Questions = []Question{{ID: "q-2", QuestionNumber: 2, Type: "填空题", Title: "新题", QuestionText: "填空", CorrectAnswer: "答案", SortOrder: 0}}
	if err := store.UpdateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetQuestionBank(ctx, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "已更新" || len(got.Questions) != 1 || got.Questions[0].ID != "q-2" || len(got.Questions[0].Options) != 0 {
		t.Fatalf("unexpected replacement: %#v", got)
	}
	if err := store.SetQuestionBankStatus(ctx, bank.ID, "disabled", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	items, total, err := func() ([]QuestionBankSummary, int, error) {
		items, err := store.ListQuestionBanks(ctx, 50, 0, "")
		if err != nil {
			return nil, 0, err
		}
		total, err := store.CountQuestionBanks(ctx, "")
		return items, total, err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || total != 1 || items[0].Status != "disabled" {
		t.Fatalf("disabled bank should remain listed: items=%d total=%d", len(items), total)
	}
}

func TestDeleteQuestionBankRemovesQuestionsAndCDKs(t *testing.T) {
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
	bank, err := store.CreateQuestionBankAuto(ctx, QuestionBank{
		Name: "待删除题库", Status: "active", CreatedAt: now, UpdatedAt: now,
		Questions: []Question{{
			ID: "q_delete_1", QuestionNumber: 1, Type: "单选题", Title: "第1题",
			QuestionText: "题干", CorrectAnswer: "A",
			Options: []QuestionOption{{ID: "opt_delete_1", Label: "A", Text: "选项A", SortOrder: 0}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateLibraryCDK(ctx, LibraryCDK{ID: "cdk_delete_1", CodeHash: "hash_delete_1", QuestionBankID: bank.ID, Status: "active", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteQuestionBank(ctx, bank.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetQuestionBank(ctx, bank.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("bank should be gone, got err=%v", err)
	}
	var questions, options, cdks int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM questions WHERE bank_id = ?", bank.ID).Scan(&questions); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM question_options WHERE question_id = ?", "q_delete_1").Scan(&options); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM library_cdks WHERE question_bank_id = ?", bank.ID).Scan(&cdks); err != nil {
		t.Fatal(err)
	}
	if questions != 0 || options != 0 || cdks != 0 {
		t.Fatalf("related rows not removed: questions=%d options=%d cdks=%d", questions, options, cdks)
	}
	if err := store.DeleteQuestionBank(ctx, bank.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second delete error = %v, want sql.ErrNoRows", err)
	}
}
