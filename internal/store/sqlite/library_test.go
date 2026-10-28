package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuestionBankCreateAndListSortsByName(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "beta", Status: "active", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.ID, "QB-") || len(first.ID) < 10 {
		t.Fatalf("unexpected bank id: %#v", first)
	}
	if _, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "alpha", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListQuestionBanks(ctx, 50, 0, "", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "alpha" || items[1].Name != "beta" {
		t.Fatalf("banks were not sorted by name: %#v", items)
	}
}

func TestUpdateQuestionBankKeepsQuestionIDs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bank, err := store.CreateQuestionBankAuto(ctx, QuestionBank{Name: "题库", Status: "active", CreatedAt: now, UpdatedAt: now, Questions: []Question{
		{ID: "q_one", QuestionNumber: 1, Type: "单选题", Title: "第一题", QuestionText: "题干一", CorrectAnswer: "A", Options: []QuestionOption{
			{ID: "opt_a", Label: "A", Text: "甲"}, {ID: "opt_b", Label: "B", Text: "乙"},
		}},
		{ID: "q_two", QuestionNumber: 2, Type: "填空题", Title: "第二题", QuestionText: "题干二", CorrectAnswer: "42"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// Edit: rename question 1, fix its option text, drop question 2 and add 3.
	updated := bank
	updated.UpdatedAt = now.Add(time.Minute)
	updated.Questions = []Question{
		{ID: "q_new1", QuestionNumber: 1, Type: "单选题", Title: "第一题（修订）", QuestionText: "题干一", CorrectAnswer: "A", Options: []QuestionOption{
			{ID: "opt_newa", Label: "A", Text: "甲（改）"}, {ID: "opt_newb", Label: "B", Text: "乙"}, {ID: "opt_newc", Label: "C", Text: "丙"},
		}},
		{ID: "q_new3", QuestionNumber: 3, Type: "填空题", Title: "第三题", QuestionText: "题干三", CorrectAnswer: "7"},
	}
	if err := store.UpdateQuestionBank(ctx, updated); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetQuestionBank(ctx, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Questions) != 2 {
		t.Fatalf("questions = %#v", stored.Questions)
	}
	first := stored.Questions[0]
	if first.ID != "q_one" || first.Title != "第一题（修订）" {
		t.Fatalf("question 1 lost its id or title: %#v", first)
	}
	options := map[string]QuestionOption{}
	for _, option := range first.Options {
		options[option.Label] = option
	}
	if options["A"].ID != "opt_a" || options["A"].Text != "甲（改）" || options["B"].ID != "opt_b" {
		t.Fatalf("options lost their ids: %#v", first.Options)
	}
	if options["C"].ID == "" || options["C"].ID == "opt_a" {
		t.Fatalf("new option needs a fresh id: %#v", options["C"])
	}
	if stored.Questions[1].ID != "q_new3" || stored.Questions[1].QuestionNumber != 3 {
		t.Fatalf("new question = %#v", stored.Questions[1])
	}
}

func TestQuestionBankRepositoryReplacesQuestionsAtomically(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bank := QuestionBank{ID: "QB-001", Name: "测试", Status: "active", CreatedAt: now, UpdatedAt: now, Questions: []Question{{ID: "q-1", QuestionNumber: 1, Type: "单选题", Title: "题目", QuestionText: "题干", CorrectAnswer: "A", SortOrder: 0, Options: []QuestionOption{{ID: "o-1", Label: "A", Text: "答案", SortOrder: 0}}}}}
	if err := store.CreateQuestionBank(ctx, bank); err != nil {
		t.Fatal(err)
	}
	original, err := store.GetQuestionBank(ctx, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := original
	unchanged.UpdatedAt = now.Add(10 * time.Minute)
	if err := store.UpdateQuestionBank(ctx, unchanged); err != nil {
		t.Fatal(err)
	}
	unchangedRead, err := store.GetQuestionBank(ctx, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchangedRead.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("unchanged content changed updated_at: before=%s after=%s", original.UpdatedAt, unchangedRead.UpdatedAt)
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
	if !got.UpdatedAt.After(original.UpdatedAt) {
		t.Fatalf("changed content did not advance updated_at: before=%s after=%s", original.UpdatedAt, got.UpdatedAt)
	}
	contentUpdatedAt := got.UpdatedAt
	if err := store.SetQuestionBankStatus(ctx, bank.ID, "disabled", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetQuestionBank(ctx, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UpdatedAt.After(contentUpdatedAt) {
		t.Fatalf("status change did not advance updated_at: before=%s after=%s", contentUpdatedAt, got.UpdatedAt)
	}
	items, total, err := func() ([]QuestionBankSummary, int, error) {
		items, err := store.ListQuestionBanks(ctx, 50, 0, "", false, "", "")
		if err != nil {
			return nil, 0, err
		}
		total, err := store.CountQuestionBanks(ctx, "", false)
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
	if err := store.InitSchema(ctx); err != nil {
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
