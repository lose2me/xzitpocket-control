package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

var questionBankIDPattern = regexp.MustCompile(`^QB-[0-9]{3,}$`)

// QuestionBankInput is used by the administrator API. The bank ID is only
// used when updating an existing bank; create requests receive a server ID.
type QuestionBankInput struct {
	ID          string          `json:"-"`
	OrderID     int             `json:"orderId,omitempty"`
	New         bool            `json:"new"`
	Name        string          `json:"name"`
	Status      string          `json:"status,omitempty"`
	RequiresCDK bool            `json:"requiresCDK"`
	Questions   []QuestionInput `json:"questions"`
}

type QuestionInput struct {
	QuestionNumber int           `json:"questionNumber"`
	Type           string        `json:"type"`
	Title          string        `json:"title"`
	QuestionText   string        `json:"questionText"`
	Options        []OptionInput `json:"options"`
	CorrectAnswer  string        `json:"correctAnswer"`
}

type OptionInput struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// QuestionBankResponse intentionally mirrors the JSON contract consumed by
// xzitpocket. Internal status and timestamps are not exposed by this response.
type QuestionBankResponse struct {
	QuestionBank QuestionBankView `json:"questionBank"`
}

type QuestionBankView struct {
	ID          string         `json:"id"`
	OrderID     int            `json:"orderId"`
	New         bool           `json:"new"`
	Name        string         `json:"name"`
	RequiresCDK bool           `json:"requiresCDK"`
	Questions   []QuestionView `json:"questions"`
}

type QuestionView struct {
	QuestionNumber int          `json:"questionNumber"`
	Type           string       `json:"type"`
	Title          string       `json:"title"`
	QuestionText   string       `json:"questionText"`
	Options        []OptionView `json:"options"`
	CorrectAnswer  string       `json:"correctAnswer"`
}

type OptionView struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type QuestionBankSummaryView struct {
	ID            string `json:"id"`
	OrderID       int    `json:"orderId"`
	New           bool   `json:"new"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	RequiresCDK   bool   `json:"requiresCDK"`
	QuestionCount int    `json:"question_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type AdminQuestionBankView struct {
	QuestionBank QuestionBankView `json:"questionBank"`
	Status       string           `json:"status"`
	CreatedAt    string           `json:"created_at"`
	UpdatedAt    string           `json:"updated_at"`
}

type LibraryCDKView struct {
	ID               string  `json:"id"`
	QuestionBankID   string  `json:"question_bank_id"`
	QuestionBankName string  `json:"question_bank_name"`
	Status           string  `json:"status"`
	BoundStudentID   string  `json:"bound_student_id,omitempty"`
	BoundUserID      string  `json:"bound_user_id,omitempty"`
	CreatedAt        string  `json:"created_at"`
	UsedAt           *string `json:"used_at,omitempty"`
	RevokedAt        *string `json:"revoked_at,omitempty"`
}

type CreatedLibraryCDKView struct {
	LibraryCDKView
	// Code is returned only from the create endpoint. It is never persisted.
	Code string `json:"code"`
}

func validateQuestionBankInput(in QuestionBankInput, requireID bool) (sqlite.QuestionBank, error) {
	in.ID = strings.TrimSpace(in.ID)
	if requireID && (in.ID == "" || !questionBankIDPattern.MatchString(in.ID)) {
		return sqlite.QuestionBank{}, Err("invalid_question_bank_id", "题库 ID 无效", http.StatusBadRequest)
	}
	if in.ID != "" && !questionBankIDPattern.MatchString(in.ID) {
		return sqlite.QuestionBank{}, Err("invalid_question_bank_id", "题库 ID 无效", http.StatusBadRequest)
	}
	if in.OrderID < 0 {
		return sqlite.QuestionBank{}, Err("invalid_question_bank_order", "顺序 ID 必须是正整数", http.StatusBadRequest)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 256 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return sqlite.QuestionBank{}, Err("invalid_question_bank", "题库名称无效", http.StatusBadRequest)
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "draft" && status != "disabled" {
		return sqlite.QuestionBank{}, Err("invalid_question_bank_status", "题库状态无效", http.StatusBadRequest)
	}
	if len(in.Questions) > 1000 {
		return sqlite.QuestionBank{}, Err("too_many_questions", "题目数量不能超过 1000", http.StatusBadRequest)
	}
	bank := sqlite.QuestionBank{ID: in.ID, OrderID: in.OrderID, IsNew: in.New, Name: in.Name, Status: status, RequiresCDK: in.RequiresCDK, Questions: make([]sqlite.Question, 0, len(in.Questions))}
	seenNumbers := make(map[int]bool, len(in.Questions))
	for index, input := range in.Questions {
		if input.QuestionNumber < 1 || input.QuestionNumber > 100000 || seenNumbers[input.QuestionNumber] {
			return sqlite.QuestionBank{}, Err("invalid_question_number", "题号必须为题库内唯一的正整数", http.StatusBadRequest)
		}
		seenNumbers[input.QuestionNumber] = true
		input.Type = strings.TrimSpace(input.Type)
		switch input.Type {
		case "单选题", "多选题", "判断题", "填空题":
		default:
			return sqlite.QuestionBank{}, Err("invalid_question_type", "题型无效", http.StatusBadRequest)
		}
		input.Title = strings.TrimSpace(input.Title)
		input.QuestionText = strings.TrimSpace(input.QuestionText)
		input.CorrectAnswer = strings.TrimSpace(input.CorrectAnswer)
		if input.Type == "" || len(input.Type) > 64 || strings.ContainsRune(input.Type, '\x00') || input.Title == "" || len(input.Title) > 256 || strings.ContainsRune(input.Title, '\x00') || input.QuestionText == "" || len(input.QuestionText) > 8192 || strings.ContainsRune(input.QuestionText, '\x00') {
			return sqlite.QuestionBank{}, Err("invalid_question", "题目内容无效", http.StatusBadRequest)
		}
		if len(input.Options) > 64 {
			return sqlite.QuestionBank{}, Err("too_many_options", "选项数量不能超过 64", http.StatusBadRequest)
		}
		qID, err := controlcrypto.NewID("q")
		if err != nil {
			return sqlite.QuestionBank{}, err
		}
		q := sqlite.Question{ID: qID, BankID: bank.ID, QuestionNumber: input.QuestionNumber, Type: input.Type, Title: input.Title, QuestionText: input.QuestionText, CorrectAnswer: input.CorrectAnswer, SortOrder: index, Options: make([]sqlite.QuestionOption, 0, len(input.Options))}
		seenLabels := make(map[string]bool, len(input.Options))
		for optionIndex, optionInput := range input.Options {
			label := strings.TrimSpace(optionInput.Label)
			text := strings.TrimSpace(optionInput.Text)
			if label == "" || len(label) > 16 || strings.ContainsAny(label, " ,\t\r\n\x00") || seenLabels[label] || text == "" || len(text) > 2048 || strings.ContainsRune(text, '\x00') {
				return sqlite.QuestionBank{}, Err("invalid_question_option", "题目选项无效", http.StatusBadRequest)
			}
			seenLabels[label] = true
			oID, err := controlcrypto.NewID("opt")
			if err != nil {
				return sqlite.QuestionBank{}, err
			}
			q.Options = append(q.Options, sqlite.QuestionOption{ID: oID, QuestionID: qID, Label: label, Text: text, SortOrder: optionIndex})
		}
		if err := validateCorrectAnswer(q.Type, q.Options, &q.CorrectAnswer); err != nil {
			return sqlite.QuestionBank{}, err
		}
		bank.Questions = append(bank.Questions, q)
	}
	sort.Slice(bank.Questions, func(i, j int) bool { return bank.Questions[i].QuestionNumber < bank.Questions[j].QuestionNumber })
	for i := range bank.Questions {
		bank.Questions[i].SortOrder = i
	}
	return bank, nil
}

func validateCorrectAnswer(questionType string, options []sqlite.QuestionOption, answer *string) error {
	labels := make(map[string]bool, len(options))
	for _, option := range options {
		labels[option.Label] = true
	}
	switch questionType {
	case "填空题":
		if len(options) != 0 || strings.TrimSpace(*answer) == "" {
			return Err("invalid_correct_answer", "填空题必须没有选项且答案不能为空", http.StatusBadRequest)
		}
		if len(*answer) > 2048 {
			return Err("invalid_correct_answer", "答案过长", http.StatusBadRequest)
		}
		return nil
	case "单选题", "判断题":
		if len(options) == 0 || !labels[*answer] {
			return Err("invalid_correct_answer", "答案必须对应一个选项标签", http.StatusBadRequest)
		}
		return nil
	case "多选题":
		if len(options) == 0 {
			return Err("invalid_correct_answer", "多选题至少需要一个选项", http.StatusBadRequest)
		}
		parts := strings.Split(*answer, ",")
		selected := make(map[string]bool, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" || !labels[part] || selected[part] {
				return Err("invalid_correct_answer", "多选题答案必须是逗号分隔的选项标签", http.StatusBadRequest)
			}
			selected[part] = true
		}
		ordered := make([]string, 0, len(selected))
		for _, option := range options {
			if selected[option.Label] {
				ordered = append(ordered, option.Label)
			}
		}
		if len(ordered) == 0 {
			return Err("invalid_correct_answer", "多选题至少选择一个答案", http.StatusBadRequest)
		}
		*answer = strings.Join(ordered, ",")
		return nil
	default:
		if len(*answer) > 2048 {
			return Err("invalid_correct_answer", "答案过长", http.StatusBadRequest)
		}
		return nil
	}
}

func questionBankView(bank sqlite.QuestionBank) QuestionBankView {
	view := QuestionBankView{ID: bank.ID, OrderID: bank.OrderID, New: bank.IsNew, Name: bank.Name, RequiresCDK: bank.RequiresCDK, Questions: make([]QuestionView, 0, len(bank.Questions))}
	sort.SliceStable(bank.Questions, func(i, j int) bool { return bank.Questions[i].QuestionNumber < bank.Questions[j].QuestionNumber })
	for _, question := range bank.Questions {
		item := QuestionView{QuestionNumber: question.QuestionNumber, Type: question.Type, Title: question.Title, QuestionText: question.QuestionText, CorrectAnswer: question.CorrectAnswer, Options: make([]OptionView, 0, len(question.Options))}
		sort.SliceStable(question.Options, func(i, j int) bool { return question.Options[i].SortOrder < question.Options[j].SortOrder })
		for _, option := range question.Options {
			item.Options = append(item.Options, OptionView{Label: option.Label, Text: option.Text})
		}
		view.Questions = append(view.Questions, item)
	}
	return view
}

func (a *App) GetQuestionBank(ctx context.Context, id string) (QuestionBankResponse, error) {
	bank, err := a.Store.GetQuestionBank(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionBankResponse{}, ErrNotFound
	}
	if err != nil {
		return QuestionBankResponse{}, err
	}
	if bank.Status != "active" {
		return QuestionBankResponse{}, ErrNotFound
	}
	return QuestionBankResponse{QuestionBank: questionBankView(bank)}, nil
}

func (a *App) ListQuestionBanks(ctx context.Context, limit, offset int, status string) ([]QuestionBankSummaryView, int, error) {
	limit = clampLimit(limit)
	if offset < 0 {
		offset = 0
	}
	status = strings.TrimSpace(status)
	if status != "" && status != "active" && status != "draft" && status != "disabled" {
		return nil, 0, Err("invalid_question_bank_status", "题库状态无效", http.StatusBadRequest)
	}
	items, err := a.Store.ListQuestionBanks(ctx, limit, offset, status)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Store.CountQuestionBanks(ctx, status)
	if err != nil {
		return nil, 0, err
	}
	result := make([]QuestionBankSummaryView, 0, len(items))
	for _, item := range items {
		result = append(result, QuestionBankSummaryView{ID: item.ID, OrderID: item.OrderID, New: item.IsNew, Name: item.Name, Status: item.Status, RequiresCDK: item.RequiresCDK, QuestionCount: item.QuestionCount, CreatedAt: item.CreatedAt.Format(time.RFC3339), UpdatedAt: item.UpdatedAt.Format(time.RFC3339)})
	}
	return result, total, nil
}

func (a *App) GetAdminQuestionBank(ctx context.Context, id string) (AdminQuestionBankView, error) {
	bank, err := a.Store.GetQuestionBank(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminQuestionBankView{}, ErrNotFound
	}
	if err != nil {
		return AdminQuestionBankView{}, err
	}
	return AdminQuestionBankView{QuestionBank: questionBankView(bank), Status: bank.Status, CreatedAt: bank.CreatedAt.Format(time.RFC3339), UpdatedAt: bank.UpdatedAt.Format(time.RFC3339)}, nil
}

func (a *App) CreateQuestionBank(ctx context.Context, in QuestionBankInput, actor string) (AdminQuestionBankView, error) {
	// Control owns question-bank IDs. A client-supplied ID is ignored on
	// create so IDs cannot be spoofed or accidentally reused.
	in.ID = ""
	bank, err := validateQuestionBankInput(in, false)
	if err != nil {
		return AdminQuestionBankView{}, err
	}
	now := time.Now().UTC()
	bank.CreatedAt, bank.UpdatedAt = now, now
	bank, err = a.Store.CreateQuestionBankAuto(ctx, bank)
	if err != nil {
		if errors.Is(err, sqlite.ErrQuestionBankOrderTaken) {
			return AdminQuestionBankView{}, Err("question_bank_order_taken", "顺序 ID 已被其他题库使用", http.StatusConflict)
		}
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return AdminQuestionBankView{}, Err("question_bank_exists", "题库 ID 已存在", http.StatusConflict)
		}
		return AdminQuestionBankView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "question_bank_create", "question_bank", bank.ID, "{}", now)
	return AdminQuestionBankView{QuestionBank: questionBankView(bank), Status: bank.Status, CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339)}, nil
}

func (a *App) UpdateQuestionBank(ctx context.Context, id string, in QuestionBankInput, actor string) (AdminQuestionBankView, error) {
	id = strings.TrimSpace(id)
	if in.ID != "" && strings.TrimSpace(in.ID) != id {
		return AdminQuestionBankView{}, Err("question_bank_id_mismatch", "题库 ID 与请求地址不一致", http.StatusBadRequest)
	}
	existing, err := a.Store.GetQuestionBank(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminQuestionBankView{}, ErrNotFound
	}
	if err != nil {
		return AdminQuestionBankView{}, err
	}
	in.ID = id
	bank, err := validateQuestionBankInput(in, true)
	if err != nil {
		return AdminQuestionBankView{}, err
	}
	now := time.Now().UTC()
	bank.CreatedAt, bank.UpdatedAt = existing.CreatedAt, now
	if bank.OrderID <= 0 {
		bank.OrderID = existing.OrderID
	}
	if err := a.Store.UpdateQuestionBank(ctx, bank); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminQuestionBankView{}, ErrNotFound
		}
		if errors.Is(err, sqlite.ErrQuestionBankOrderTaken) {
			return AdminQuestionBankView{}, Err("question_bank_order_taken", "顺序 ID 已被其他题库使用", http.StatusConflict)
		}
		return AdminQuestionBankView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "question_bank_update", "question_bank", bank.ID, "{}", now)
	return AdminQuestionBankView{QuestionBank: questionBankView(bank), Status: bank.Status, CreatedAt: bank.CreatedAt.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339)}, nil
}

// DisableQuestionBank keeps the bank visible to administrators with an
// explicit disabled status.
func (a *App) DisableQuestionBank(ctx context.Context, id, actor string) error {
	now := time.Now().UTC()
	if err := a.Store.DisableQuestionBank(ctx, strings.TrimSpace(id), now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = a.Store.AddAudit(ctx, actor, "question_bank_disable", "question_bank", id, "{}", now)
	return nil
}
