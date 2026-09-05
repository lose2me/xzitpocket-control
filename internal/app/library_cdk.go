package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

const libraryCDKCodePrefix = "CDK-"

type LibraryCDKCreateInput struct {
	Count int `json:"count,omitempty"`
}

type LibraryCDKRedemption struct {
	Redeemed       bool   `json:"redeemed"`
	QuestionBankID string `json:"question_bank_id"`
	Status         string `json:"status"`
}

func canonicalLibraryCDKCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	if strings.HasPrefix(value, "CDK") {
		return libraryCDKCodePrefix + strings.ReplaceAll(value[3:], "-", "")
	}
	return value
}

func validLibraryCDKCode(value string) bool {
	if len(value) != len(libraryCDKCodePrefix)+16 || !strings.HasPrefix(value, libraryCDKCodePrefix) {
		return false
	}
	for _, r := range value[len(libraryCDKCodePrefix):] {
		if (r < 'A' || r > 'Z') && (r < '2' || r > '7') {
			return false
		}
	}
	return true
}

func newLibraryCDKCode() (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return libraryCDKCodePrefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]), nil
}

func (a *App) CreateLibraryCDK(ctx context.Context, in LibraryCDKCreateInput, actor string) (CreatedLibraryCDKView, error) {
	items, err := a.CreateLibraryCDKs(ctx, in, actor)
	if err != nil {
		return CreatedLibraryCDKView{}, err
	}
	return items[0], nil
}

func (a *App) CreateLibraryCDKs(ctx context.Context, in LibraryCDKCreateInput, actor string) ([]CreatedLibraryCDKView, error) {
	count := in.Count
	if count <= 0 {
		count = 1
	}
	if count > 500 {
		return nil, Err("too_many_library_cdks", "单次最多生成 500 个 CDK", http.StatusBadRequest)
	}
	now := time.Now().UTC()
	for attempt := 0; attempt < 5; attempt++ {
		cdks := make([]sqlite.LibraryCDK, 0, count)
		codes := make([]string, 0, count)
		for i := 0; i < count; i++ {
			code, err := newLibraryCDKCode()
			if err != nil {
				return nil, err
			}
			id, err := controlcrypto.NewID("cdk")
			if err != nil {
				return nil, err
			}
			cdks = append(cdks, sqlite.LibraryCDK{ID: id, CodeHash: controlcrypto.HashToken(a.Cfg.TokenPepper, canonicalLibraryCDKCode(code)), Status: "active", CreatedAt: now})
			codes = append(codes, code)
		}
		if err := a.Store.CreateLibraryCDKs(ctx, cdks); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return nil, err
		}
		views := make([]CreatedLibraryCDKView, 0, len(cdks))
		for i, cdk := range cdks {
			views = append(views, CreatedLibraryCDKView{LibraryCDKView: libraryCDKView(cdk, a.Cfg.EncryptionKey), Code: codes[i]})
			_ = a.Store.AddAudit(ctx, actor, "library_cdk_create", "library_cdk", cdk.ID, controlcrypto.JSON(map[string]any{"batch_count": count}), now)
		}
		return views, nil
	}
	return nil, errors.New("could not generate a unique library cdk")
}

func libraryCDKView(cdk sqlite.LibraryCDK, encryptionKey []byte) LibraryCDKView {
	view := LibraryCDKView{ID: cdk.ID, QuestionBankID: cdk.QuestionBankID, Status: cdk.Status, BoundUserID: cdk.BoundUserID, CreatedAt: cdk.CreatedAt.Format(time.RFC3339)}
	if cdk.BoundStudentIDCiphertext != "" {
		if student, err := controlcrypto.Decrypt(encryptionKey, cdk.BoundStudentIDCiphertext); err == nil {
			view.BoundStudentID = student
		}
	}
	if cdk.UsedAt != nil {
		value := cdk.UsedAt.Format(time.RFC3339)
		view.UsedAt = &value
	}
	return view
}

func (a *App) ListLibraryCDKs(ctx context.Context, limit, offset int, search ...string) ([]LibraryCDKView, int, error) {
	query := ""
	if len(search) > 0 {
		query = strings.TrimSpace(search[0])
	}
	studentHash := ""
	if controlcrypto.ValidateStudentID(query) == nil {
		studentHash = controlcrypto.HMACHex(a.Cfg.IDPepper, query)
	}
	codeHash := ""
	canonicalCode := canonicalLibraryCDKCode(query)
	if validLibraryCDKCode(canonicalCode) {
		codeHash = controlcrypto.HashToken(a.Cfg.TokenPepper, canonicalCode)
	}
	items, total, err := a.Store.ListLibraryCDKs(ctx, limit, offset, query, studentHash, codeHash)
	if err != nil {
		return nil, 0, err
	}
	result := make([]LibraryCDKView, 0, len(items))
	for _, item := range items {
		result = append(result, libraryCDKView(item, a.Cfg.EncryptionKey))
	}
	return result, total, nil
}

func (a *App) RedeemLibraryCDK(ctx context.Context, p SessionPrincipal, code, bankID string) (LibraryCDKRedemption, error) {
	code = canonicalLibraryCDKCode(code)
	if !validLibraryCDKCode(code) {
		return LibraryCDKRedemption{}, Err("invalid_library_cdk", "CDK 格式无效", http.StatusBadRequest)
	}
	bankID = strings.TrimSpace(bankID)
	if !questionBankIDPattern.MatchString(bankID) {
		return LibraryCDKRedemption{}, Err("invalid_question_bank_id", "题库 ID 无效", http.StatusBadRequest)
	}
	bank, bankErr := a.Store.GetQuestionBank(ctx, bankID)
	if errors.Is(bankErr, sql.ErrNoRows) {
		return LibraryCDKRedemption{}, ErrNotFound
	}
	if bankErr != nil {
		return LibraryCDKRedemption{}, bankErr
	}
	if bank.Status != "active" || !bank.RequiresCDK {
		return LibraryCDKRedemption{}, Err("invalid_library_target", "请选择需要 CDK 的启用题库", http.StatusBadRequest)
	}
	identity, err := a.Store.GetIdentityByUser(ctx, p.User.ID, identityProvider)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryCDKRedemption{}, Err("student_identity_missing", "当前用户没有学号身份", http.StatusForbidden)
	}
	if err != nil {
		return LibraryCDKRedemption{}, err
	}
	cdk, err := a.Store.RedeemLibraryCDK(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, code), bankID, identity.StudentIDHash, identity.StudentIDCiphertext, p.User.ID, time.Now().UTC())
	if errors.Is(err, sqlite.ErrLibraryCDKNotFound) {
		return LibraryCDKRedemption{}, Err("library_cdk_not_found", "CDK 不存在", http.StatusNotFound)
	}
	if errors.Is(err, sqlite.ErrLibraryCDKDisabled) {
		return LibraryCDKRedemption{}, Err("library_cdk_disabled", "CDK 已禁用", http.StatusConflict)
	}
	if errors.Is(err, sqlite.ErrLibraryCDKBound) {
		return LibraryCDKRedemption{}, Err("library_cdk_bound", "CDK 已绑定其他学号", http.StatusConflict)
	}
	if err != nil {
		return LibraryCDKRedemption{}, err
	}
	_ = a.Store.AddAudit(ctx, p.User.ID, "library_cdk_redeem", "library_cdk", cdk.ID, "{}", time.Now().UTC())
	return LibraryCDKRedemption{Redeemed: true, QuestionBankID: cdk.QuestionBankID, Status: cdk.Status}, nil
}

func (a *App) SetLibraryCDKStatus(ctx context.Context, id, status, actor string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound
	}
	if status != "active" && status != "disabled" {
		return Err("invalid_library_cdk_status", "CDK 状态无效", http.StatusBadRequest)
	}
	if err := a.Store.SetLibraryCDKStatus(ctx, id, status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = a.Store.AddAudit(ctx, actor, "library_cdk_status", "library_cdk", id, controlcrypto.JSON(map[string]string{"status": status}), time.Now().UTC())
	return nil
}

func (a *App) GetQuestionBankForUser(ctx context.Context, id, userID string) (QuestionBankResponse, error) {
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
	if bank.RequiresCDK {
		identity, identityErr := a.Store.GetIdentityByUser(ctx, userID, identityProvider)
		if errors.Is(identityErr, sql.ErrNoRows) {
			return QuestionBankResponse{}, Err("question_bank_locked", "题库需要 CDK 解锁", http.StatusForbidden)
		}
		if identityErr != nil {
			return QuestionBankResponse{}, identityErr
		}
		allowed, accessErr := a.Store.HasLibraryAccess(ctx, bank.ID, identity.StudentIDHash)
		if accessErr != nil {
			return QuestionBankResponse{}, accessErr
		}
		if !allowed {
			return QuestionBankResponse{}, Err("question_bank_locked", "题库需要 CDK 解锁", http.StatusForbidden)
		}
	}
	return QuestionBankResponse{QuestionBank: questionBankView(bank)}, nil
}

func (a *App) ListQuestionBanksForUser(ctx context.Context, limit, offset int, userID string) ([]QuestionBankSummaryView, int, error) {
	items, total, err := a.Store.ListAccessibleQuestionBanks(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	result := make([]QuestionBankSummaryView, 0, len(items))
	for _, item := range items {
		result = append(result, QuestionBankSummaryView{ID: item.ID, OrderID: item.OrderID, New: item.IsNew, Name: item.Name, Status: item.Status, RequiresCDK: item.RequiresCDK, QuestionCount: item.QuestionCount, CreatedAt: item.CreatedAt.Format(time.RFC3339), UpdatedAt: item.UpdatedAt.Format(time.RFC3339)})
	}
	return result, total, nil
}
