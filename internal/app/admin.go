package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

// AdminLogin authenticates the sole local administrator with the management key.
func (a *App) AdminLogin(ctx context.Context, key string) (string, AdminPrincipal, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", AdminPrincipal{}, Err("invalid_credentials", "管理密钥错误", http.StatusUnauthorized)
	}
	admin, err := a.Store.GetAdmin(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", AdminPrincipal{}, Err("invalid_credentials", "管理密钥错误", http.StatusUnauthorized)
	}
	if err != nil {
		return "", AdminPrincipal{}, err
	}
	if admin.DisabledAt != nil || !controlcrypto.Argon2idVerify(admin.PasswordHash, key) {
		return "", AdminPrincipal{}, Err("invalid_credentials", "管理密钥错误", http.StatusUnauthorized)
	}
	token, err := controlcrypto.NewToken(32)
	if err != nil {
		return "", AdminPrincipal{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(12 * time.Hour)
	if err := a.Store.CreateAdminSession(ctx, admin.ID, controlcrypto.HashToken(a.Cfg.TokenPepper, token), expires, now); err != nil {
		return "", AdminPrincipal{}, err
	}
	_ = a.Store.TouchAdmin(ctx, admin.ID, now)
	_ = a.Store.AddAudit(ctx, admin.ID, "admin_login", "admin", admin.ID, "{}", now)
	admin.LastLoginAt = now
	return token, AdminPrincipal{Admin: admin, Token: token}, nil
}

func (a *App) RevokeAdmin(ctx context.Context, p AdminPrincipal) error {
	now := time.Now().UTC()
	if err := a.Store.RevokeAdminSession(ctx, p.Session.ID, now); err != nil {
		return err
	}
	_ = a.Store.AddAudit(ctx, p.Admin.ID, "admin_logout", "admin", p.Admin.ID, "{}", now)
	return nil
}

func (a *App) ListUsers(ctx context.Context, limit, offset int, status string) ([]sqlite.UserListItem, int, error) {
	limit = clampLimit(limit)
	if offset < 0 {
		offset = 0
	}
	users, err := a.Store.ListUsers(ctx, limit, offset, status)
	if err != nil {
		return nil, 0, err
	}
	count, err := a.Store.CountUsers(ctx, status)
	return users, count, err
}

type UserDetail struct {
	User      UserView     `json:"user"`
	StudentID string       `json:"student_id,omitempty"`
	Devices   []DeviceView `json:"devices"`
}

func (a *App) GetUserDetail(ctx context.Context, id string) (UserDetail, error) {
	u, err := a.Store.GetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return UserDetail{}, ErrNotFound
	}
	if err != nil {
		return UserDetail{}, err
	}
	identity, err := a.Store.GetIdentityByUser(ctx, id, identityProvider)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return UserDetail{}, err
	}
	studentID := ""
	alias := ""
	if identity.UserID != "" {
		alias = identity.StudentAlias
		studentID, _ = controlcrypto.Decrypt(a.Cfg.EncryptionKey, identity.StudentIDCiphertext)
	}
	devices, err := a.Store.ListUserDevices(ctx, id)
	if err != nil {
		return UserDetail{}, err
	}
	return UserDetail{User: toUserView(u, alias), StudentID: studentID, Devices: userDevicesView(devices)}, nil
}

func clampLimit(v int) int {
	if v <= 0 {
		return 50
	}
	if v > 200 {
		return 200
	}
	return v
}

func (a *App) ListDevices(ctx context.Context, limit, offset int) ([]DeviceView, int, error) {
	limit = clampLimit(limit)
	if offset < 0 {
		offset = 0
	}
	devices, err := a.Store.ListDevices(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	count, err := a.Store.CountDevices(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]DeviceView, 0, len(devices))
	for _, d := range devices {
		out = append(out, toDeviceView(d))
	}
	return out, count, nil
}

func (a *App) ListRisk(ctx context.Context, limit, offset int, acknowledged *bool) ([]sqlite.RiskEvent, error) {
	return a.Store.ListRiskEvents(ctx, clampLimit(limit), max0(offset), acknowledged)
}
func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
func (a *App) AcknowledgeRisk(ctx context.Context, id string) error {
	if err := a.Store.AcknowledgeRiskEvent(ctx, id, time.Now().UTC()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
func (a *App) ListAudit(ctx context.Context, limit, offset int) ([]sqlite.AuditLog, error) {
	return a.Store.ListAudit(ctx, clampLimit(limit), max0(offset))
}
func (a *App) SetUserStatus(ctx context.Context, id, status, actor string) error {
	status = strings.TrimSpace(status)
	if status != "active" && status != "disabled" {
		return Err("invalid_user_status", "用户状态无效", http.StatusBadRequest)
	}
	if _, err := a.Store.GetUser(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := a.Store.SetUserStatus(ctx, id, status); err != nil {
		return err
	}
	return a.Store.AddAudit(ctx, actor, "user_status_change", "user", id, controlcrypto.JSON(map[string]string{"status": status}), time.Now().UTC())
}
