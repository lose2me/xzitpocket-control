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

// LibraryCDKCreateInput identifies the one question bank unlocked by a new
// code. A code is intentionally single-purpose and cannot be retargeted.
type LibraryCDKCreateInput struct {
	QuestionBankID string `json:"question_bank_id"`
	Count          int    `json:"count,omitempty"`
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
		payload := strings.ReplaceAll(value[3:], "-", "")
		return libraryCDKCodePrefix + payload
	}
	return value
}

func validLibraryCDKCode(value string) bool {
	if len(value) < len(libraryCDKCodePrefix)+8 || len(value) > 128 || !strings.HasPrefix(value, libraryCDKCodePrefix) {
		return false
	}
	for _, r := range value[len(libraryCDKCodePrefix):] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func newLibraryCDKCode() (string, error) {
	var raw [15]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	// Grouping improves transcription while canonicalization remains trivial.
	return libraryCDKCodePrefix + encoded[:4] + "-" + encoded[4:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func (a *App) CreateLibraryCDK(ctx context.Context, in LibraryCDKCreateInput, actor string) (CreatedLibraryCDKView, error) {
	items, err := a.CreateLibraryCDKs(ctx, in, actor)
	if err != nil {
		return CreatedLibraryCDKView{}, err
	}
	return items[0], nil
}

// CreateLibraryCDKs generates one or more independent codes for the selected
// question bank. Plaintext codes are returned only from this response.
func (a *App) CreateLibraryCDKs(ctx context.Context, in LibraryCDKCreateInput, actor string) ([]CreatedLibraryCDKView, error) {
	bankID := strings.TrimSpace(in.QuestionBankID)
	if !questionBankIDPattern.MatchString(bankID) {
		return nil, Err("invalid_question_bank_id", "题库 ID 无效", http.StatusBadRequest)
	}
	bank, err := a.Store.GetQuestionBank(ctx, bankID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if bank.Status != "active" {
		return nil, Err("question_bank_unavailable", "题库未启用，不能生成 CDK", http.StatusConflict)
	}
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
			code, codeErr := newLibraryCDKCode()
			if codeErr != nil {
				return nil, codeErr
			}
			cdkID, idErr := controlcrypto.NewID("cdk")
			if idErr != nil {
				return nil, idErr
			}
			cdks = append(cdks, sqlite.LibraryCDK{ID: cdkID, CodeHash: controlcrypto.HashToken(a.Cfg.TokenPepper, canonicalLibraryCDKCode(code)), QuestionBankID: bankID, Status: "active", CreatedAt: now})
			codes = append(codes, code)
		}
		err = a.Store.CreateLibraryCDKs(ctx, cdks)
		if err == nil {
			views := make([]CreatedLibraryCDKView, 0, len(cdks))
			for i, cdk := range cdks {
				view := libraryCDKView(cdk, a.Cfg.EncryptionKey)
				view.QuestionBankName = bank.Name
				views = append(views, CreatedLibraryCDKView{LibraryCDKView: view, Code: codes[i]})
				_ = a.Store.AddAudit(ctx, actor, "library_cdk_create", "library_cdk", cdk.ID, controlcrypto.JSON(map[string]any{"question_bank_id": bankID, "batch_count": count}), now)
			}
			return views, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, err
		}
	}
	return nil, errors.New("could not generate a unique library cdk")
}

func libraryCDKView(cdk sqlite.LibraryCDK, encryptionKey []byte) LibraryCDKView {
	view := LibraryCDKView{ID: cdk.ID, QuestionBankID: cdk.QuestionBankID, QuestionBankName: cdk.QuestionBankName, Status: cdk.Status, BoundUserID: cdk.BoundUserID, CreatedAt: cdk.CreatedAt.Format(time.RFC3339)}
	if cdk.BoundStudentIDCiphertext != "" {
		if student, err := controlcrypto.Decrypt(encryptionKey, cdk.BoundStudentIDCiphertext); err == nil {
			view.BoundStudentID = student
		}
	}
	if cdk.UsedAt != nil {
		value := cdk.UsedAt.Format(time.RFC3339)
		view.UsedAt = &value
	}
	if cdk.RevokedAt != nil {
		value := cdk.RevokedAt.Format(time.RFC3339)
		view.RevokedAt = &value
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

func (a *App) RedeemLibraryCDK(ctx context.Context, p SessionPrincipal, code string) (LibraryCDKRedemption, error) {
	code = canonicalLibraryCDKCode(code)
	if !validLibraryCDKCode(code) {
		return LibraryCDKRedemption{}, Err("invalid_library_cdk", "CDK 格式无效", http.StatusBadRequest)
	}
	identity, err := a.Store.GetIdentityByUser(ctx, p.User.ID, identityProvider)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryCDKRedemption{}, Err("student_identity_missing", "当前用户没有学号身份", http.StatusForbidden)
	}
	if err != nil {
		return LibraryCDKRedemption{}, err
	}
	studentCiphertext := identity.StudentIDCiphertext
	cdk, err := a.Store.RedeemLibraryCDK(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, code), identity.StudentIDHash, studentCiphertext, p.User.ID, time.Now().UTC())
	if errors.Is(err, sqlite.ErrLibraryCDKNotFound) {
		return LibraryCDKRedemption{}, Err("library_cdk_not_found", "CDK 不存在", http.StatusNotFound)
	}
	if errors.Is(err, sqlite.ErrLibraryCDKRevoked) {
		return LibraryCDKRedemption{}, Err("library_cdk_revoked", "CDK 已撤销", http.StatusConflict)
	}
	if errors.Is(err, sqlite.ErrLibraryCDKBound) {
		return LibraryCDKRedemption{}, Err("library_cdk_bound", "CDK 已绑定其他学号", http.StatusConflict)
	}
	if err != nil {
		return LibraryCDKRedemption{}, err
	}
	_ = a.Store.AddAudit(ctx, p.User.ID, "library_cdk_redeem", "library_cdk", cdk.ID, controlcrypto.JSON(map[string]string{"question_bank_id": cdk.QuestionBankID}), time.Now().UTC())
	return LibraryCDKRedemption{Redeemed: true, QuestionBankID: cdk.QuestionBankID, Status: cdk.Status}, nil
}

func (a *App) RevokeLibraryCDK(ctx context.Context, id, actor string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound
	}
	if err := a.Store.RevokeLibraryCDK(ctx, id, time.Now().UTC()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if errors.Is(err, sqlite.ErrLibraryCDKUnavailable) {
			return Err("library_cdk_unavailable", "CDK 已使用或撤销", http.StatusConflict)
		}
		return err
	}
	_ = a.Store.AddAudit(ctx, actor, "library_cdk_revoke", "library_cdk", id, "{}", time.Now().UTC())
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
	identity, err := a.Store.GetIdentityByUser(ctx, userID, identityProvider)
	if errors.Is(err, sql.ErrNoRows) {
		identity.StudentIDHash = ""
	} else if err != nil {
		return nil, 0, err
	}
	items, total, err := a.Store.ListAccessibleQuestionBanks(ctx, limit, offset, identity.StudentIDHash)
	if err != nil {
		return nil, 0, err
	}
	result := make([]QuestionBankSummaryView, 0, len(items))
	for _, item := range items {
		result = append(result, QuestionBankSummaryView{ID: item.ID, OrderID: item.OrderID, New: item.IsNew, Name: item.Name, Status: item.Status, RequiresCDK: item.RequiresCDK, QuestionCount: item.QuestionCount, CreatedAt: item.CreatedAt.Format(time.RFC3339), UpdatedAt: item.UpdatedAt.Format(time.RFC3339)})
	}
	return result, total, nil
}
