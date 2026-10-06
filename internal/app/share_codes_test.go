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

func TestShareCodeRoundTripAndExpiry(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{
		TokenPepper:   []byte("token-pepper"),
		IDPepper:      []byte("id-pepper"),
		EncryptionKey: []byte("01234567890123456789012345678901"),
		AdminKey:      "test",
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateShareCode(ctx, ShareCodeCreateInput{Suffix: "2", Data: map[string]any{"version": 1, "value": "配置"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Code) != 6 || created.Code[5] != '2' || created.ExpiresAt.Sub(created.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("unexpected share code metadata: %#v", created)
	}
	data, meta, err := a.ReadShareCode(ctx, "  "+created.Code+"  ")
	if err != nil {
		t.Fatal(err)
	}
	if data["value"] != "配置" || meta.ID == "" {
		t.Fatalf("unexpected share code payload: %#v %#v", data, meta)
	}
	if _, _, err := a.ReadShareCode(ctx, "123456"); err == nil {
		t.Fatal("unknown share code should fail")
	}
	if _, err := store.DB.ExecContext(ctx, "UPDATE share_codes SET expires_at = ? WHERE id = ?", time.Now().UTC().Add(-time.Hour).UnixMilli(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.ReadShareCode(ctx, created.Code); err == nil || err.(*APIError).Code != "share_code_expired" {
		t.Fatalf("expired share code error = %v", err)
	}
}

// The admin list labels each share code with its creator's student number, and
// administrator-created codes have no student number at all.
func TestListShareCodesResolvesStudentID(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	encryptionKey := []byte("01234567890123456789012345678901")
	a, err := New(config.Config{
		TokenPepper:   []byte("token-pepper"),
		IDPepper:      []byte("id-pepper"),
		EncryptionKey: encryptionKey,
		AdminKey:      "test",
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := controlcrypto.Encrypt(encryptionKey, "2025001234")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	userID := "usr_share_test"
	if _, err := store.DB.ExecContext(ctx, "INSERT INTO users(id, status, display_name, college_name, class_name, created_at, last_login_at) VALUES (?, 'active', '测试学生', '', '', ?, ?)", userID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO identities(id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at)
		VALUES ('idn_share_test', ?, ?, 'hash', '', ?, ?, ?)`, userID, identityProvider, ciphertext, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	first, err := a.CreateShareCode(ctx, ShareCodeCreateInput{Suffix: "1", Data: map[string]any{"v": 1}}, userID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.CreateShareCode(ctx, ShareCodeCreateInput{Suffix: "1", Data: map[string]any{"v": 1}}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Code != first.Code || !second.ExpiresAt.After(first.ExpiresAt.Add(-time.Second)) {
		t.Fatalf("same user/payload did not reuse and renew share code: first=%#v second=%#v", first, second)
	}
	if _, err := a.CreateShareCode(ctx, ShareCodeCreateInput{Suffix: "2", Data: map[string]any{"v": 2}}, ""); err != nil {
		t.Fatal(err)
	}
	views, total, err := a.ListShareCodes(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(views) != 2 {
		t.Fatalf("total = %d, views = %d", total, len(views))
	}
	byStudentID := map[string]string{}
	for _, view := range views {
		if view.CreatedByUserID == userID && view.StudentID != "2025001234" {
			t.Fatalf("student id = %q, want 2025001234", view.StudentID)
		}
		if view.CreatedByUserID == "" && view.StudentID != "" {
			t.Fatalf("anonymous share code student id = %q, want empty", view.StudentID)
		}
		byStudentID[view.CreatedByUserID] = view.StudentID
	}
	if byStudentID[""] != "" {
		t.Fatalf("unexpected ids: %#v", byStudentID)
	}
}

// Administrators can read a share code back after creation and inspect the
// decrypted payload, even though only the code hash is used for lookups.
func TestShareCodeListAndDetailExposeCode(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{
		TokenPepper:   []byte("token-pepper"),
		IDPepper:      []byte("id-pepper"),
		EncryptionKey: []byte("01234567890123456789012345678901"),
		AdminKey:      "test",
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateShareCode(ctx, ShareCodeCreateInput{Suffix: "2", Data: map[string]any{"settings": map[string]any{"theme": "dark"}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	views, total, err := a.ListShareCodes(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(views) != 1 {
		t.Fatalf("total = %d, views = %d", total, len(views))
	}
	if views[0].Code != created.Code {
		t.Fatalf("listed code = %q, want %q", views[0].Code, created.Code)
	}
	detail, data, err := a.GetShareCode(ctx, views[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Code != created.Code || detail.ID != views[0].ID {
		t.Fatalf("detail = %#v", detail)
	}
	settings, ok := data["settings"].(map[string]any)
	if !ok || settings["theme"] != "dark" {
		t.Fatalf("detail payload = %#v", data)
	}
	if _, _, err := a.GetShareCode(ctx, "share_missing"); err == nil {
		t.Fatal("missing share code should fail")
	}
}
