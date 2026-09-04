package sqlite

import (
	"context"
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
	if err := store.DisableQuestionBank(ctx, bank.ID, now.Add(2*time.Minute)); err != nil {
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
