package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

const shareCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type ShareCodeCreateInput struct {
	Data map[string]any `json:"data"`
}

type ShareCodeView struct {
	ID              string    `json:"id"`
	Code            string    `json:"code,omitempty"`
	CreatedByUserID string    `json:"created_by_user_id,omitempty"`
	StudentID       string    `json:"student_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type CreatedShareCodeView struct {
	Code      string    `json:"code"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func canonicalShareCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func validShareCode(value string) bool {
	value = canonicalShareCode(value)
	if len(value) != 6 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune(shareCodeAlphabet, char) {
			return false
		}
	}
	return true
}

func newShareCode() (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var code strings.Builder
	code.Grow(len(raw))
	for _, value := range raw {
		code.WriteByte(shareCodeAlphabet[int(value)%len(shareCodeAlphabet)])
	}
	return code.String(), nil
}

func (a *App) CreateShareCode(ctx context.Context, in ShareCodeCreateInput, userID string) (CreatedShareCodeView, error) {
	if len(in.Data) == 0 {
		return CreatedShareCodeView{}, Err("invalid_share_data", "分享数据不能为空", http.StatusBadRequest)
	}
	payload, err := json.Marshal(in.Data)
	if err != nil {
		return CreatedShareCodeView{}, Err("invalid_share_data", "分享数据格式无效", http.StatusBadRequest)
	}
	if len(payload) > 512<<10 {
		return CreatedShareCodeView{}, Err("share_data_too_large", "分享数据不能超过 512 KB", http.StatusRequestEntityTooLarge)
	}
	encrypted, err := controlcrypto.Encrypt(a.Cfg.EncryptionKey, string(payload))
	if err != nil {
		return CreatedShareCodeView{}, err
	}
	now := time.Now().UTC()
	for attempt := 0; attempt < 8; attempt++ {
		code, err := newShareCode()
		if err != nil {
			return CreatedShareCodeView{}, err
		}
		id, err := controlcrypto.NewID("share")
		if err != nil {
			return CreatedShareCodeView{}, err
		}
		codeCiphertext, err := a.encryptShareCode(code)
		if err != nil {
			return CreatedShareCodeView{}, err
		}
		item := sqlite.ShareCode{
			ID:                id,
			CodeHash:          controlcrypto.HashToken(a.Cfg.TokenPepper, canonicalShareCode(code)),
			CodeCiphertext:    codeCiphertext,
			PayloadCiphertext: encrypted,
			CreatedByUserID:   userID,
			CreatedAt:         now,
			ExpiresAt:         now.Add(7 * 24 * time.Hour),
		}
		if err := a.Store.CreateShareCode(ctx, item); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return CreatedShareCodeView{}, err
		}
		return CreatedShareCodeView{Code: code, CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt}, nil
	}
	return CreatedShareCodeView{}, errors.New("could not generate a unique share code")
}

func (a *App) ReadShareCode(ctx context.Context, rawCode string) (map[string]any, ShareCodeView, error) {
	code := canonicalShareCode(rawCode)
	if !validShareCode(code) {
		return nil, ShareCodeView{}, Err("invalid_share_code", "分享码无效", http.StatusBadRequest)
	}
	item, err := a.Store.GetShareCodeByHash(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ShareCodeView{}, Err("share_code_not_found", "分享码不存在或已过期", http.StatusNotFound)
	}
	if err != nil {
		return nil, ShareCodeView{}, err
	}
	if !item.ExpiresAt.After(time.Now().UTC()) {
		return nil, ShareCodeView{}, Err("share_code_expired", "分享码已过期", http.StatusGone)
	}
	plain, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, item.PayloadCiphertext)
	if err != nil {
		return nil, ShareCodeView{}, Err("invalid_share_data", "分享数据已损坏", http.StatusInternalServerError)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(plain), &data); err != nil || data == nil {
		return nil, ShareCodeView{}, Err("invalid_share_data", "分享数据格式无效", http.StatusInternalServerError)
	}
	return data, ShareCodeView{ID: item.ID, CreatedByUserID: item.CreatedByUserID, CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt}, nil
}

// The plaintext code is never stored as-is; keeping an encrypted copy lets
// administrators look a code up again after creation.
func (a *App) encryptShareCode(code string) (string, error) {
	return controlcrypto.Encrypt(a.Cfg.EncryptionKey, canonicalShareCode(code))
}

func (a *App) decryptShareCode(ciphertext string) string {
	if strings.TrimSpace(ciphertext) == "" {
		return ""
	}
	code, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, ciphertext)
	if err != nil {
		return ""
	}
	return canonicalShareCode(code)
}

func (a *App) shareCodeView(ctx context.Context, item sqlite.ShareCode) ShareCodeView {
	return ShareCodeView{
		ID:              item.ID,
		Code:            a.decryptShareCode(item.CodeCiphertext),
		CreatedByUserID: item.CreatedByUserID,
		StudentID:       a.studentIDForUser(ctx, item.CreatedByUserID),
		CreatedAt:       item.CreatedAt,
		ExpiresAt:       item.ExpiresAt,
	}
}

func (a *App) ListShareCodes(ctx context.Context, limit, offset int) ([]ShareCodeView, int, error) {
	items, total, err := a.Store.ListShareCodes(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	views := make([]ShareCodeView, 0, len(items))
	for _, item := range items {
		views = append(views, a.shareCodeView(ctx, item))
	}
	return views, total, nil
}

// GetShareCode returns one share code together with its decrypted payload for
// the administrator console.
func (a *App) GetShareCode(ctx context.Context, id string) (ShareCodeView, map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ShareCodeView{}, nil, Err("invalid_share_code", "分享码标识无效", http.StatusBadRequest)
	}
	item, err := a.Store.GetShareCodeByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ShareCodeView{}, nil, ErrNotFound
	}
	if err != nil {
		return ShareCodeView{}, nil, err
	}
	plain, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, item.PayloadCiphertext)
	if err != nil {
		return ShareCodeView{}, nil, Err("invalid_share_data", "分享数据已损坏", http.StatusInternalServerError)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(plain), &data); err != nil {
		return ShareCodeView{}, nil, Err("invalid_share_data", "分享数据格式无效", http.StatusInternalServerError)
	}
	return a.shareCodeView(ctx, item), data, nil
}

// studentIDForUser resolves the decrypted student number behind a user ID.
// Administrator-created share codes have no user, so the result stays empty.
func (a *App) studentIDForUser(ctx context.Context, userID string) string {
	if strings.TrimSpace(userID) == "" {
		return ""
	}
	identity, err := a.Store.GetIdentityByUser(ctx, userID, identityProvider)
	if err != nil {
		return ""
	}
	studentID, _ := controlcrypto.Decrypt(a.Cfg.EncryptionKey, identity.StudentIDCiphertext)
	return studentID
}

func (a *App) DeleteShareCode(ctx context.Context, id string, actor string) error {
	if strings.TrimSpace(id) == "" {
		return Err("invalid_share_code", "分享码标识无效", http.StatusBadRequest)
	}
	if err := a.Store.DeleteShareCode(ctx, id); err != nil {
		return err
	}
	return a.Store.AddAudit(ctx, actor, "share_code_delete", "share_code", id, "{}", time.Now().UTC())
}
