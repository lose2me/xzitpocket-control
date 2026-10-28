package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"xzitpocket-control/internal/app"
	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

func newAdminServer(t *testing.T) (*httptest.Server, map[string]string, string) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateUser(ctx, sqlite.User{ID: "user", Status: "active", CreatedAt: now, LastLoginAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDevice(ctx, sqlite.Device{ID: "device", DeviceSerial: "serial", Installation: "installation", TokenHash: "device-hash", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte("test-token-pepper")
	if err := store.CreateSession(ctx, sqlite.Session{ID: "session", UserID: "user", DeviceID: "device", AccessHash: controlcrypto.HashToken(pepper, "access"), RefreshHash: "refresh-hash", ExpiresAt: now.Add(time.Hour), RefreshExpires: now.Add(24 * time.Hour), CreatedAt: now, LastUsedAt: now}); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := controlcrypto.Encrypt([]byte("01234567890123456789012345678901"), "20260001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO identities(id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at) VALUES ('identity', 'user', 'xzit-oa', ?, '', ?, ?, ?)`, controlcrypto.HMACHex([]byte("test-id-pepper"), "20260001"), ciphertext, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.New(config.Config{TokenPepper: pepper, IDPepper: []byte("test-id-pepper"), EncryptionKey: []byte("01234567890123456789012345678901"), AdminKey: "test-password"}, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(a, http.NotFoundHandler(), logger))
	t.Cleanup(ts.Close)
	admin := requestWithHeaders(t, ts.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "test-password"}, nil)
	return ts, map[string]string{"Authorization": "Bearer access", "X-Student-ID": "20260001"}, "Bearer " + admin["access_token"].(string)
}

func TestCourseUploadAndAdminCRUD(t *testing.T) {
	ts, userHeaders, adminToken := newAdminServer(t)
	adminHeaders := map[string]string{"Authorization": adminToken}

	body := map[string]any{"items": []any{
		map[string]any{"code": "3007B2106", "name": "数学分析（Ⅲ）", "books": []any{map[string]any{"name": "数学分析", "tags": "高教社"}}},
		map[string]any{"code": "3007B2106", "name": "重复编号"},
	}}
	if status := requestStatus(t, ts.URL+"/api/v1/courses", http.MethodPost, body, map[string]string{"X-Student-ID": "20260001"}); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated course upload status = %d, want %d", status, http.StatusUnauthorized)
	}
	uploaded := requestWithHeaders(t, ts.URL+"/api/v1/courses", http.MethodPost, body, userHeaders)
	if uploaded["accepted"].(float64) != 1 || uploaded["accepted_books"].(float64) != 1 {
		t.Fatalf("course upload = %#v", uploaded)
	}

	listed := requestWithHeaders(t, ts.URL+"/api/v1/admin/courses?q=3007", http.MethodGet, nil, adminHeaders)
	items := listed["items"].([]any)
	if listed["total"].(float64) != 1 || len(items) != 1 {
		t.Fatalf("admin course list = %#v", listed)
	}
	books := items[0].(map[string]any)["books"].([]any)
	if len(books) != 1 || books[0].(map[string]any)["name"] != "数学分析" {
		t.Fatalf("derived course books missing: %#v", items[0])
	}

	created := requestWithHeaders(t, ts.URL+"/api/v1/admin/courses", http.MethodPost, map[string]any{"code": "3007P2108", "name": "计算机网络"}, adminHeaders)
	courseID := created["id"].(string)
	if courseID == "" {
		t.Fatalf("admin course create = %#v", created)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/courses/"+courseID, http.MethodPut, map[string]any{"code": "3007P2108", "name": "计算机网络（改）"}, adminHeaders); status != http.StatusOK {
		t.Fatalf("admin course update status = %d, want %d", status, http.StatusOK)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/courses/"+courseID, http.MethodDelete, nil, adminHeaders); status != http.StatusOK {
		t.Fatalf("admin course delete status = %d, want %d", status, http.StatusOK)
	}
}
