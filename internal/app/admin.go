package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

func (a *App) AdminLogin(ctx context.Context, username, password string) (string, AdminPrincipal, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", AdminPrincipal{}, Err("invalid_credentials", "用户名或密码错误", http.StatusUnauthorized)
	}
	admin, err := a.Store.GetAdminByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", AdminPrincipal{}, Err("invalid_credentials", "用户名或密码错误", http.StatusUnauthorized)
	}
	if err != nil {
		return "", AdminPrincipal{}, err
	}
	if admin.DisabledAt != nil || !controlcrypto.Argon2idVerify(admin.PasswordHash, password) {
		return "", AdminPrincipal{}, Err("invalid_credentials", "用户名或密码错误", http.StatusUnauthorized)
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
	User          UserView           `json:"user"`
	StudentID     string             `json:"student_id,omitempty"`
	Devices       []DeviceView       `json:"devices"`
	OAuthAccounts []OAuthAccountView `json:"oauth_accounts"`
}

type OAuthAccountView struct {
	ID              string  `json:"id"`
	ProviderID      string  `json:"provider_id"`
	ExternalSubject string  `json:"external_subject,omitempty"`
	DisplayName     string  `json:"display_name"`
	Scope           string  `json:"scope"`
	ExpiresAt       *string `json:"expires_at,omitempty"`
	UpdatedAt       string  `json:"updated_at"`
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
	accounts, err := a.Store.ListOAuthAccounts(ctx, id)
	if err != nil {
		return UserDetail{}, err
	}
	accountViews := make([]OAuthAccountView, 0, len(accounts))
	for _, account := range accounts {
		var expires *string
		if account.ExpiresAt.Valid {
			value := time.UnixMilli(account.ExpiresAt.Int64).UTC().Format(time.RFC3339)
			expires = &value
		}
		accountViews = append(accountViews, OAuthAccountView{ID: account.ID, ProviderID: account.ProviderID, ExternalSubject: account.ExternalSubject, DisplayName: account.DisplayName, Scope: account.Scope, ExpiresAt: expires, UpdatedAt: account.UpdatedAt.Format(time.RFC3339)})
	}
	return UserDetail{User: toUserView(u, alias), StudentID: studentID, Devices: userDevicesView(devices), OAuthAccounts: accountViews}, nil
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
	if _, err := a.Store.GetUser(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := a.Store.SetUserStatus(ctx, id, status); err != nil {
		return err
	}
	if status == "deleted" {
		if err := a.Store.AnonymizeUser(ctx, id); err != nil {
			return err
		}
	}
	return a.Store.AddAudit(ctx, actor, "user_status_change", "user", id, controlcrypto.JSON(map[string]string{"status": status}), time.Now().UTC())
}

type OAuthClientInput struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	ClientSecret string   `json:"client_secret"`
	RedirectURIs []string `json:"redirect_uris"`
	Scopes       []string `json:"scopes"`
	Status       string   `json:"status"`
}

func (a *App) UpsertOAuthClient(ctx context.Context, in OAuthClientInput) (sqlite.OAuthClient, error) {
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.ClientName = strings.TrimSpace(in.ClientName)
	if !validIdentifier(in.ClientID) || in.ClientName == "" {
		return sqlite.OAuthClient{}, Err("invalid_oauth_client", "OAuth 客户端参数无效", http.StatusBadRequest)
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "disabled" {
		return sqlite.OAuthClient{}, Err("invalid_status", "状态无效", http.StatusBadRequest)
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 20 {
		return sqlite.OAuthClient{}, Err("invalid_redirect_uris", "回调地址无效", http.StatusBadRequest)
	}
	for _, u := range in.RedirectURIs {
		if strings.TrimSpace(u) == "" || strings.ContainsAny(u, "\r\n") {
			return sqlite.OAuthClient{}, Err("invalid_redirect_uris", "回调地址无效", http.StatusBadRequest)
		}
		parsed, parseErr := url.Parse(u)
		if parseErr != nil || parsed.Fragment != "" || parsed.Scheme == "" || parsed.Scheme == "javascript" || parsed.Scheme == "data" || ((parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host == "") {
			return sqlite.OAuthClient{}, Err("invalid_redirect_uris", "回调地址无效", http.StatusBadRequest)
		}
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return sqlite.OAuthClient{}, Err("invalid_scope", "scope 无效", http.StatusBadRequest)
	}
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile"}
	}
	redirectJSON, _ := json.Marshal(in.RedirectURIs)
	scopeJSON, _ := json.Marshal(scopes)
	now := time.Now().UTC()
	existing, err := a.Store.GetOAuthClient(ctx, in.ClientID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sqlite.OAuthClient{}, err
	}
	created := now
	secretHash := existing.SecretHash
	if existing.CreatedAt.IsZero() == false {
		created = existing.CreatedAt
	}
	if in.ClientSecret != "" {
		secretHash = sql.NullString{String: controlcrypto.HashToken(a.Cfg.TokenPepper, in.ClientSecret), Valid: true}
	}
	c := sqlite.OAuthClient{ClientID: in.ClientID, ClientName: in.ClientName, SecretHash: secretHash, RedirectURIs: string(redirectJSON), Scopes: string(scopeJSON), Status: in.Status, CreatedAt: created, UpdatedAt: now}
	if err := a.Store.UpsertOAuthClient(ctx, c); err != nil {
		return sqlite.OAuthClient{}, err
	}
	return c, nil
}

type OAuthProviderInput struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	AuthorizationURL string   `json:"authorization_url"`
	TokenURL         string   `json:"token_url"`
	UserinfoURL      string   `json:"userinfo_url"`
	ClientID         string   `json:"client_id"`
	ClientSecret     string   `json:"client_secret"`
	Scopes           []string `json:"scopes"`
	Status           string   `json:"status"`
}

func (a *App) UpsertOAuthProvider(ctx context.Context, in OAuthProviderInput) (sqlite.OAuthProvider, error) {
	in.ID = strings.TrimSpace(in.ID)
	if !validIdentifier(in.ID) || in.Name == "" || in.AuthorizationURL == "" || in.TokenURL == "" || in.ClientID == "" {
		return sqlite.OAuthProvider{}, Err("invalid_oauth_provider", "OAuth Provider 参数无效", http.StatusBadRequest)
	}
	for _, endpoint := range []string{in.AuthorizationURL, in.TokenURL, in.UserinfoURL} {
		if endpoint == "" {
			continue
		}
		u, parseErr := url.Parse(endpoint)
		if parseErr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return sqlite.OAuthProvider{}, Err("invalid_oauth_provider", "OAuth Provider 地址无效", http.StatusBadRequest)
		}
	}
	if in.Status == "" {
		in.Status = "active"
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return sqlite.OAuthProvider{}, Err("invalid_scope", "scope 无效", http.StatusBadRequest)
	}
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile"}
	}
	scopeJSON, _ := json.Marshal(scopes)
	now := time.Now().UTC()
	existing, err := a.Store.GetOAuthProvider(ctx, in.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sqlite.OAuthProvider{}, err
	}
	secret := existing.SecretCiphertext
	if in.ClientSecret != "" {
		secret, err = controlcrypto.Encrypt(a.Cfg.EncryptionKey, in.ClientSecret)
		if err != nil {
			return sqlite.OAuthProvider{}, err
		}
	}
	created := now
	if !existing.CreatedAt.IsZero() {
		created = existing.CreatedAt
	}
	p := sqlite.OAuthProvider{ID: in.ID, Name: in.Name, AuthorizationURL: in.AuthorizationURL, TokenURL: in.TokenURL, UserinfoURL: sql.NullString{String: in.UserinfoURL, Valid: in.UserinfoURL != ""}, ClientID: in.ClientID, SecretCiphertext: secret, Scopes: string(scopeJSON), Status: in.Status, CreatedAt: created, UpdatedAt: now}
	if err := a.Store.UpsertOAuthProvider(ctx, p); err != nil {
		return sqlite.OAuthProvider{}, err
	}
	return p, nil
}

func (a *App) ListOAuthClients(ctx context.Context) ([]sqlite.OAuthClient, error) {
	return a.Store.ListOAuthClients(ctx)
}
func (a *App) ListOAuthProviders(ctx context.Context) ([]sqlite.OAuthProvider, error) {
	return a.Store.ListOAuthProviders(ctx)
}
func (a *App) ListServiceClients(ctx context.Context) ([]sqlite.ServiceClient, error) {
	return a.Store.ListServiceClients(ctx)
}

type ServiceClientInput struct {
	Audience string   `json:"audience"`
	Secret   string   `json:"secret"`
	Scopes   []string `json:"scopes"`
	Status   string   `json:"status"`
}

func (a *App) UpsertServiceClient(ctx context.Context, in ServiceClientInput) (sqlite.ServiceClient, error) {
	in.Audience = strings.TrimSpace(in.Audience)
	if !validIdentifier(in.Audience) {
		return sqlite.ServiceClient{}, Err("invalid_service", "服务名称无效", http.StatusBadRequest)
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "disabled" {
		return sqlite.ServiceClient{}, Err("invalid_status", "状态无效", http.StatusBadRequest)
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return sqlite.ServiceClient{}, Err("invalid_scope", "scope 无效", http.StatusBadRequest)
	}
	if len(scopes) == 0 {
		return sqlite.ServiceClient{}, Err("invalid_scope", "至少配置一个 scope", http.StatusBadRequest)
	}
	existing, err := a.Store.GetServiceClientByAudience(ctx, in.Audience)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sqlite.ServiceClient{}, err
	}
	now := time.Now().UTC()
	id := existing.ID
	if id == "" {
		id, err = controlcrypto.NewID("svc")
		if err != nil {
			return sqlite.ServiceClient{}, err
		}
	}
	secret := existing.SecretCiphertext
	if in.Secret != "" {
		cipher, e := controlcrypto.Encrypt(a.Cfg.EncryptionKey, in.Secret)
		if e != nil {
			return sqlite.ServiceClient{}, e
		}
		secret = sql.NullString{String: cipher, Valid: true}
	}
	data, _ := json.Marshal(scopes)
	out := sqlite.ServiceClient{ID: id, Audience: in.Audience, SecretCiphertext: secret, Scopes: string(data), Status: in.Status, CreatedAt: existing.CreatedAt, UpdatedAt: now}
	if out.CreatedAt.IsZero() {
		out.CreatedAt = now
	}
	if err := a.Store.UpsertServiceClient(ctx, out); err != nil {
		return sqlite.ServiceClient{}, err
	}
	return out, nil
}

func (a *App) ValidateServiceClientSecret(ctx context.Context, audience, clientID, secret string) error {
	client, err := a.Store.GetServiceClientByAudience(ctx, audience)
	if err != nil || client.Status != "active" || client.ID != clientID || !client.SecretCiphertext.Valid {
		return Err("invalid_client", "服务客户端认证失败", http.StatusUnauthorized)
	}
	plain, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, client.SecretCiphertext.String)
	if err != nil || !controlcrypto.ConstantTimeEqual(plain, secret) {
		return Err("invalid_client", "服务客户端认证失败", http.StatusUnauthorized)
	}
	return nil
}
