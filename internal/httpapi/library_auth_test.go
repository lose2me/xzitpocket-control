package httpapi

import (
	"context"
	"encoding/json"
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

func TestQuestionReaderAuthentication(t *testing.T) {
	for _, test := range []struct {
		name                          string
		disabled, revoked, expired    bool
		deviceRevoked, admin, noToken bool
		unknown                       bool
		status                        int
		code                          string
	}{
		{name: "active", status: http.StatusOK},
		{name: "missing token", noToken: true, status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "unknown token", unknown: true, status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "expired", expired: true, status: http.StatusUnauthorized, code: "access_token_expired"},
		{name: "expired disabled", disabled: true, expired: true, status: http.StatusUnauthorized, code: "access_token_expired"},
		{name: "revoked active", revoked: true, status: http.StatusUnauthorized, code: "access_token_expired"},
		{name: "disabled", disabled: true, status: http.StatusForbidden, code: "user_unavailable"},
		{name: "disabled revoked", disabled: true, revoked: true, status: http.StatusForbidden, code: "user_unavailable"},
		{name: "device revoked", deviceRevoked: true, status: http.StatusUnauthorized, code: "device_revoked"},
		{name: "disabled device revoked", disabled: true, revoked: true, deviceRevoked: true, status: http.StatusUnauthorized, code: "device_revoked"},
		{name: "admin preview fallback", disabled: true, revoked: true, admin: true, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			old := now.Add(-time.Hour)
			userStatus := "active"
			if test.disabled {
				userStatus = "disabled"
			}
			if err := store.CreateUser(ctx, sqlite.User{ID: "user", Status: userStatus, CreatedAt: old, LastLoginAt: old}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateDevice(ctx, sqlite.Device{ID: "device", DeviceSerial: "serial", Installation: "installation", TokenHash: "device-hash", CreatedAt: old, LastSeenAt: old}); err != nil {
				t.Fatal(err)
			}
			pepper := []byte("test-token-pepper")
			accessHash := controlcrypto.HashToken(pepper, "access")
			expires := now.Add(time.Hour)
			if test.expired {
				expires = old
			}
			if err := store.CreateSession(ctx, sqlite.Session{ID: "session", UserID: "user", DeviceID: "device", AccessHash: accessHash, RefreshHash: "refresh-hash", ExpiresAt: expires, RefreshExpires: now.Add(24 * time.Hour), CreatedAt: old, LastUsedAt: old}); err != nil {
				t.Fatal(err)
			}
			if test.revoked {
				if err := store.RevokeSession(ctx, "session", now); err != nil {
					t.Fatal(err)
				}
			}
			if test.deviceRevoked {
				if err := store.SetDeviceRevoked(ctx, "device", true, now); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.CreateQuestionBank(ctx, sqlite.QuestionBank{ID: "QB-001", Name: "题库", Status: "active", RequiresCDK: test.admin, CreatedAt: old, UpdatedAt: old}); err != nil {
				t.Fatal(err)
			}
			headers := map[string]string{"Authorization": "Bearer access"}
			if test.noToken {
				delete(headers, "Authorization")
			} else if test.unknown {
				headers["Authorization"] = "Bearer unknown"
			} else if test.admin {
				if err := store.CreateAdmin(ctx, sqlite.Admin{ID: "admin", PasswordHash: "unused", CreatedAt: old, LastLoginAt: old}); err != nil {
					t.Fatal(err)
				}
				if err := store.CreateAdminSession(ctx, "admin", controlcrypto.HashToken(pepper, "admin-access"), expires, old); err != nil {
					t.Fatal(err)
				}
				delete(headers, "Authorization")
				headers["Cookie"] = "control_access=access; control_admin=admin-access"
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			a := &app.App{Store: store, Cfg: config.Config{TokenPepper: pepper}, Logger: logger}
			ts := httptest.NewServer(New(a, nil, logger))
			defer ts.Close()
			for _, path := range []string{"/api/v1/question-banks", "/api/v1/question-banks/QB-001"} {
				if test.status == http.StatusOK {
					requestWithHeaders(t, ts.URL+path, http.MethodGet, nil, headers)
				} else {
					requestFailure(t, ts.URL+path, headers, test.status, test.code)
				}
			}
			if test.status != http.StatusOK || test.admin {
				session, err := store.GetSessionByAccessHash(ctx, accessHash)
				if err != nil || !session.LastUsedAt.Equal(old) {
					t.Fatalf("rejected session was touched: %#v %v", session, err)
				}
				device, err := store.GetDeviceByID(ctx, "device")
				if err != nil || !device.LastSeenAt.Equal(old) {
					t.Fatalf("rejected device was touched: %#v %v", device, err)
				}
				user, err := store.GetUser(ctx, "user")
				if err != nil || !user.LastLoginAt.Equal(old) {
					t.Fatalf("rejected user was touched: %#v %v", user, err)
				}
			}
		})
	}
}

func requestFailure(t *testing.T, endpoint string, headers map[string]string, status int, code string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status || out.Error.Code != code {
		t.Fatalf("GET %s -> %d %q, want %d %q", endpoint, resp.StatusCode, out.Error.Code, status, code)
	}
}
