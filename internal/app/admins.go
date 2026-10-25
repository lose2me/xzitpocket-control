package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

const (
	superAdminName  = "admin"
	superAdminPerms = "*=rw"
)

var adminNamePattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// AdminSummaryView is the administrator list row shown in the console.
type AdminSummaryView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Permissions string `json:"permissions"`
	Super       bool   `json:"super"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at"`
}

// AdminWriteInput is the create/update payload. Key is optional on update; an
// empty key keeps the existing one.
type AdminWriteInput struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Permissions string `json:"permissions"`
}

func (a *App) adminSummary(item sqlite.Admin) AdminSummaryView {
	return AdminSummaryView{
		ID: item.ID, Name: item.Name, Key: a.adminKey(item), Permissions: item.Permissions, Super: item.IsSuperAdmin(),
		CreatedAt: item.CreatedAt.Format(time.RFC3339), LastLoginAt: item.LastLoginAt.Format(time.RFC3339),
	}
}

// adminKey returns the plaintext key for the super-admin-only account list.
// The super admin's key comes from the running config; console-created accounts
// are decrypted from the stored copy.
func (a *App) adminKey(item sqlite.Admin) string {
	if item.Name == superAdminName {
		return strings.TrimSpace(a.Cfg.AdminKey)
	}
	if item.KeyCiphertext == "" {
		return ""
	}
	key, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, item.KeyCiphertext)
	if err != nil {
		return ""
	}
	return key
}

func (a *App) ListAdminsForAdmin(ctx context.Context) ([]AdminSummaryView, error) {
	items, err := a.Store.ListAdmins(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]AdminSummaryView, 0, len(items))
	for _, item := range items {
		views = append(views, a.adminSummary(item))
	}
	return views, nil
}

func (a *App) CreateAdminForAdmin(ctx context.Context, in AdminWriteInput, actor string) (AdminSummaryView, error) {
	// The console no longer asks for an administrator name: accounts are
	// identified by their key. A name is still stored for internal use and is
	// generated from the account ID when the caller omits it.
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if name != "" {
		if !adminNamePattern.MatchString(name) {
			return AdminSummaryView{}, Err("invalid_admin", "管理员名称需为 1-32 位字母、数字、下划线或短横线", http.StatusBadRequest)
		}
		if name == superAdminName {
			return AdminSummaryView{}, Err("invalid_admin", "不能新建超级管理员", http.StatusBadRequest)
		}
	}
	key := strings.TrimSpace(in.Key)
	if len(key) < 10 || len(key) > 128 {
		return AdminSummaryView{}, Err("invalid_admin_key", "管理密钥长度需为 10-128", http.StatusBadRequest)
	}
	perms, err := config.ParseAdminPerms(in.Permissions)
	if err != nil {
		return AdminSummaryView{}, Err("invalid_admin_permissions", err.Error(), http.StatusBadRequest)
	}
	if perms["*"] == "rw" {
		return AdminSummaryView{}, Err("invalid_admin_permissions", "不能授予通配权限", http.StatusBadRequest)
	}
	if name != "" {
		if _, err := a.Store.GetAdminByName(ctx, name); err == nil {
			return AdminSummaryView{}, Err("admin_exists", "该管理员已存在", http.StatusConflict)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return AdminSummaryView{}, err
		}
	}
	hash, err := controlcrypto.Argon2idHash(key)
	if err != nil {
		return AdminSummaryView{}, err
	}
	ciphertext, err := controlcrypto.Encrypt(a.Cfg.EncryptionKey, key)
	if err != nil {
		return AdminSummaryView{}, err
	}
	id, err := controlcrypto.NewID("adm")
	if err != nil {
		return AdminSummaryView{}, err
	}
	if name == "" {
		name = strings.ToLower(id)
	}
	now := time.Now().UTC()
	item := sqlite.Admin{
		ID: id, Name: name, Permissions: adminPermsString(perms),
		PasswordHash: hash, KeyCiphertext: ciphertext, CreatedAt: now, LastLoginAt: now,
	}
	if err := a.Store.CreateAdmin(ctx, item); err != nil {
		return AdminSummaryView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "admin_create", "admin", id, "{}", now)
	return a.adminSummary(item), nil
}

func (a *App) UpdateAdminForAdmin(ctx context.Context, id string, in AdminWriteInput, actor string) (AdminSummaryView, error) {
	item, err := a.Store.GetAdminByID(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminSummaryView{}, ErrNotFound
	}
	if err != nil {
		return AdminSummaryView{}, err
	}
	if item.IsSuperAdmin() || item.Name == superAdminName {
		return AdminSummaryView{}, Err("super_admin", "超级管理员只能在 .env 修改", http.StatusForbidden)
	}
	perms, err := config.ParseAdminPerms(in.Permissions)
	if err != nil {
		return AdminSummaryView{}, Err("invalid_admin_permissions", err.Error(), http.StatusBadRequest)
	}
	if perms["*"] == "rw" {
		return AdminSummaryView{}, Err("invalid_admin_permissions", "不能授予通配权限", http.StatusBadRequest)
	}
	hash := item.PasswordHash
	ciphertext := ""
	if key := strings.TrimSpace(in.Key); key != "" {
		if len(key) < 10 || len(key) > 128 {
			return AdminSummaryView{}, Err("invalid_admin_key", "管理密钥长度需为 10-128", http.StatusBadRequest)
		}
		hash, err = controlcrypto.Argon2idHash(key)
		if err != nil {
			return AdminSummaryView{}, err
		}
		ciphertext, err = controlcrypto.Encrypt(a.Cfg.EncryptionKey, key)
		if err != nil {
			return AdminSummaryView{}, err
		}
	}
	now := time.Now().UTC()
	if err := a.Store.UpdateAdminAuth(ctx, item.ID, hash, adminPermsString(perms), ciphertext); err != nil {
		return AdminSummaryView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "admin_update", "admin", item.ID, "{}", now)
	updated, err := a.Store.GetAdminByID(ctx, item.ID)
	if err != nil {
		return AdminSummaryView{}, err
	}
	return a.adminSummary(updated), nil
}

func (a *App) DeleteAdminForAdmin(ctx context.Context, id, actor string) error {
	item, err := a.Store.GetAdminByID(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if item.IsSuperAdmin() || item.Name == superAdminName {
		return Err("super_admin", "不能删除超级管理员", http.StatusForbidden)
	}
	if err := a.Store.DeleteAdmin(ctx, item.ID); err != nil {
		return err
	}
	_ = a.Store.AddAudit(ctx, actor, "admin_delete", "admin", item.ID, "{}", time.Now().UTC())
	return nil
}
