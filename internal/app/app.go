package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
}

func New(cfg config.Config, store *sqlite.Store, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	a := &App{Cfg: cfg, Store: store, Logger: logger}
	if err := a.ensureAdmin(context.Background()); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) ensureAdmin(ctx context.Context) error {
	count, err := a.Store.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	hash, err := controlcrypto.Argon2idHash(a.Cfg.AdminKey)
	if err != nil {
		return fmt.Errorf("hash administrator key: %w", err)
	}
	now := time.Now().UTC()
	id, err := controlcrypto.NewID("adm")
	if err != nil {
		return err
	}
	if err := a.Store.CreateAdmin(ctx, sqlite.Admin{ID: id, PasswordHash: hash, CreatedAt: now, LastLoginAt: now}); err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	if a.Cfg.AdminKey == "change-me" {
		a.Logger.Warn("using default admin management key; edit data/.env")
	}
	return nil
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
	_ = a.Store.TouchDevice(ctx, d.ID, time.Now().UTC())
	return DevicePrincipal{Device: d, Token: token}, nil
}

func (a *App) AuthenticateSession(ctx context.Context, token string) (SessionPrincipal, error) {
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
	if session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		return SessionPrincipal{}, Err("access_token_expired", "访问令牌已过期", http.StatusUnauthorized)
	}
	user, err := a.Store.GetUser(ctx, session.UserID)
	if err != nil {
		return SessionPrincipal{}, err
	}
	device, err := a.Store.GetDeviceByID(ctx, session.DeviceID)
	if err != nil {
		return SessionPrincipal{}, err
	}
	if device.RevokedAt != nil {
		return SessionPrincipal{}, Err("device_revoked", "设备已撤销", http.StatusUnauthorized)
	}
	_ = a.Store.TouchSession(ctx, session.ID, now)
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
