package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func Err(code, message string, status int) *APIError {
	return &APIError{Code: code, Message: message, Status: status}
}

var (
	ErrNotFound     = Err("not_found", "资源不存在", http.StatusNotFound)
	ErrUnauthorized = Err("unauthorized", "未授权", http.StatusUnauthorized)
	ErrForbidden    = Err("forbidden", "无权访问", http.StatusForbidden)
)

type App struct {
	Cfg    config.Config
	Store  *sqlite.Store
	Logger *slog.Logger

	cdnMu    sync.Mutex
	cdnCache cdnStatsCache

	hostMu    sync.Mutex
	hostCache hostStatsCache

	bucketMu    sync.Mutex
	bucketCache bucketStatsCache
}

func New(cfg config.Config, store *sqlite.Store, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	a := &App{Cfg: cfg, Store: store, Logger: logger}
	if err := a.ensureAdmins(context.Background()); err != nil {
		return nil, err
	}
	return a, nil
}

// ensureAdmins keeps the .env super administrator in sync. Additional admins
// are managed in the console: the database is authoritative once any exist, so
// deleting an admin in the console is never undone by a restart. .env accounts
// are only seeded into a fresh database.
func (a *App) ensureAdmins(ctx context.Context) error {
	now := time.Now().UTC()
	accounts := make([]config.AdminAccount, 0, len(a.Cfg.Admins)+1)
	if key := strings.TrimSpace(a.Cfg.AdminKey); key != "" {
		accounts = append(accounts, config.AdminAccount{Name: "admin", Key: key, All: true})
	}
	count, err := a.Store.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		for _, account := range a.Cfg.Admins {
			if account.Name != "admin" {
				accounts = append(accounts, account)
			}
		}
	}
	for _, account := range accounts {
		hash, err := controlcrypto.Argon2idHash(account.Key)
		if err != nil {
			return fmt.Errorf("hash admin %q: %w", account.Name, err)
		}
		perms := adminPermsString(account)
		existing, err := a.Store.GetAdminByName(ctx, account.Name)
		if errors.Is(err, sql.ErrNoRows) && account.Name == "admin" {
			// Adopt the legacy administrator row created before names existed.
			if first, firstErr := a.Store.GetAdmin(ctx); firstErr == nil && first.Name == "" {
				existing, err = first, nil
			}
		}
		switch {
		case err == nil:
			if existing.Name != account.Name {
				if err := a.Store.SetAdminName(ctx, existing.ID, account.Name); err != nil {
					return fmt.Errorf("rename admin %q: %w", account.Name, err)
				}
			}
			if err := a.Store.UpdateAdminAuth(ctx, existing.ID, hash, perms); err != nil {
				return fmt.Errorf("update admin %q: %w", account.Name, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			id, idErr := controlcrypto.NewID("adm")
			if idErr != nil {
				return idErr
			}
			if createErr := a.Store.CreateAdmin(ctx, sqlite.Admin{
				ID: id, Name: account.Name, Permissions: perms, PasswordHash: hash, CreatedAt: now, LastLoginAt: now,
			}); createErr != nil {
				return fmt.Errorf("create admin %q: %w", account.Name, createErr)
			}
		default:
			return err
		}
	}
	if len(accounts) == 0 {
		a.Logger.Warn("no admin accounts configured; the console has no super admin")
	}
	if a.Cfg.AdminKey == "change-me" {
		a.Logger.Warn("using default admin management key; edit data/.env")
	}
	return nil
}

func adminPermsString(account config.AdminAccount) string {
	if account.All {
		return "*=rw"
	}
	parts := make([]string, 0, len(account.Perms))
	for page, access := range account.Perms {
		parts = append(parts, page+"="+access)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

type DevicePrincipal struct {
	Device sqlite.Device
	Token  string
}

type SessionPrincipal struct {
	Session sqlite.Session
	User    sqlite.User
	Device  sqlite.Device
	Token   string
}

type AdminPrincipal struct {
	Admin   sqlite.Admin
	Session sqlite.AdminSession
	Token   string
}

func (a *App) AuthenticateDevice(ctx context.Context, token string) (DevicePrincipal, error) {
	if strings.TrimSpace(token) == "" {
		return DevicePrincipal{}, ErrUnauthorized
	}
	d, err := a.Store.GetDeviceByTokenHash(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, token))
	if errors.Is(err, sql.ErrNoRows) {
		return DevicePrincipal{}, Err("invalid_device_token", "设备令牌无效", http.StatusUnauthorized)
	}
	if err != nil {
		return DevicePrincipal{}, err
	}
	if d.RevokedAt != nil {
		return DevicePrincipal{}, Err("device_revoked", "设备已撤销", http.StatusUnauthorized)
	}
	now := time.Now().UTC()
	if err := a.Store.TouchDevice(ctx, d.ID, now); err != nil {
		a.Logger.Warn("touch device failed", "device_id", d.ID, "error", err)
	}
	d.LastSeenAt = now
	return DevicePrincipal{Device: d, Token: token}, nil
}

func (a *App) AuthenticateSession(ctx context.Context, token string) (SessionPrincipal, error) {
	return a.authenticateSession(ctx, token, false)
}

// AuthenticateLibrarySession preserves the library's account-status error even
// when disabling the account has revoked its sessions. No rejected session is
// accepted or touched; expired tokens and revoked devices remain unauthorized.
func (a *App) AuthenticateLibrarySession(ctx context.Context, token string) (SessionPrincipal, error) {
	return a.authenticateSession(ctx, token, true)
}

func (a *App) authenticateSession(ctx context.Context, token string, library bool) (SessionPrincipal, error) {
	if strings.TrimSpace(token) == "" {
		return SessionPrincipal{}, ErrUnauthorized
	}
	session, err := a.Store.GetSessionByAccessHash(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, token))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionPrincipal{}, Err("invalid_access_token", "访问令牌无效", http.StatusUnauthorized)
	}
	if err != nil {
		return SessionPrincipal{}, err
	}
	now := time.Now().UTC()
	if !session.ExpiresAt.After(now) || (!library && session.RevokedAt != nil) {
		return SessionPrincipal{}, Err("access_token_expired", "访问令牌已过期", http.StatusUnauthorized)
	}
	user, err := a.Store.GetUser(ctx, session.UserID)
	if err != nil {
		return SessionPrincipal{}, err
	}
	if !library && user.Status != "active" {
		return SessionPrincipal{}, Err("account_disabled", "账号已被停用，无法使用当前会话", http.StatusForbidden)
	}
	device, err := a.Store.GetDeviceByID(ctx, session.DeviceID)
	if err != nil {
		return SessionPrincipal{}, err
	}
	if device.RevokedAt != nil {
		return SessionPrincipal{}, Err("device_revoked", "设备已撤销", http.StatusUnauthorized)
	}
	if library && user.Status != "active" {
		return SessionPrincipal{}, Err("user_unavailable", "您的账户被暂时禁用", http.StatusForbidden)
	}
	if session.RevokedAt != nil {
		return SessionPrincipal{}, Err("access_token_expired", "访问令牌已过期", http.StatusUnauthorized)
	}
	if err := a.Store.TouchSession(ctx, session.ID, now); err != nil {
		a.Logger.Warn("touch session failed", "session_id", session.ID, "error", err)
	}
	if err := a.Store.TouchDevice(ctx, device.ID, now); err != nil {
		a.Logger.Warn("touch device failed", "device_id", device.ID, "error", err)
	}
	if err := a.Store.TouchUserConnection(ctx, user.ID, now); err != nil {
		a.Logger.Warn("touch control user connection failed", "user_id", user.ID, "error", err)
	}
	session.LastUsedAt = now
	device.LastSeenAt = now
	user.LastLoginAt = now
	return SessionPrincipal{Session: session, User: user, Device: device, Token: token}, nil
}

func (a *App) AuthenticateAdmin(ctx context.Context, token string) (AdminPrincipal, error) {
	if strings.TrimSpace(token) == "" {
		return AdminPrincipal{}, ErrUnauthorized
	}
	session, err := a.Store.GetAdminSession(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, token))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminPrincipal{}, Err("invalid_admin_session", "管理员会话无效", http.StatusUnauthorized)
	}
	if err != nil {
		return AdminPrincipal{}, err
	}
	now := time.Now().UTC()
	if session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		return AdminPrincipal{}, Err("admin_session_expired", "管理员会话已过期", http.StatusUnauthorized)
	}
	admin, err := a.Store.GetAdminByID(ctx, session.AdminID)
	if err != nil {
		return AdminPrincipal{}, err
	}
	if admin.DisabledAt != nil {
		return AdminPrincipal{}, ErrForbidden
	}
	_ = a.Store.TouchAdminSession(ctx, session.ID, now)
	return AdminPrincipal{Admin: admin, Session: session, Token: token}, nil
}

func (a *App) Close() error { return a.Store.Close() }
