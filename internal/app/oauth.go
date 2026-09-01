package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

var oauthDefaultScopes = []string{"openid", "profile"}

type AuthorizeInput struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

func (a *App) Authorize(ctx context.Context, p SessionPrincipal, in AuthorizeInput) (string, error) {
	client, err := a.Store.GetOAuthClient(ctx, in.ClientID)
	if errors.Is(err, sql.ErrNoRows) || client.Status != "active" {
		return "", Err("invalid_client", "OAuth 客户端无效", http.StatusBadRequest)
	}
	if err != nil {
		return "", err
	}
	if in.ResponseType != "code" {
		return "", Err("unsupported_response_type", "仅支持 authorization code", http.StatusBadRequest)
	}
	if !containsExact(parseJSONStrings(client.RedirectURIs), in.RedirectURI) {
		return "", Err("invalid_redirect_uri", "回调地址未注册", http.StatusBadRequest)
	}
	if in.CodeChallenge == "" || in.CodeChallengeMethod != "S256" {
		return "", Err("pkce_required", "必须使用 PKCE S256", http.StatusBadRequest)
	}
	scopes, err := parseAndValidateScope(in.Scope, parseJSONStrings(client.Scopes))
	if err != nil {
		return "", err
	}
	code, err := controlcrypto.NewToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	codeObj := sqlite.OAuthCode{CodeHash: controlcrypto.HashToken(a.Cfg.TokenPepper, code), ClientID: client.ClientID, UserID: p.User.ID, DeviceID: sql.NullString{String: p.Device.ID, Valid: true}, RedirectURI: in.RedirectURI, Scope: strings.Join(scopes, " "), CodeChallenge: sql.NullString{String: in.CodeChallenge, Valid: true}, CodeChallengeMethod: sql.NullString{String: "S256", Valid: true}, ExpiresAt: now.Add(5 * time.Minute)}
	if err := a.Store.CreateOAuthCode(ctx, codeObj); err != nil {
		return "", err
	}
	u, err := url.Parse(in.RedirectURI)
	if err != nil {
		return "", Err("invalid_redirect_uri", "回调地址无效", http.StatusBadRequest)
	}
	q := u.Query()
	q.Set("code", code)
	if in.State != "" {
		q.Set("state", in.State)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseJSONStrings(raw string) []string {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil
	}
	return values
}
func containsExact(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func parseAndValidateScope(raw string, allowed []string) ([]string, error) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		parts = append([]string{}, oauthDefaultScopes...)
	}
	normalized, err := normalizeScopes(parts)
	if err != nil {
		return nil, Err("invalid_scope", "scope 无效", http.StatusBadRequest)
	}
	if !containsAll(allowed, normalized) {
		return nil, Err("invalid_scope", "请求的 scope 未被允许", http.StatusBadRequest)
	}
	return normalized, nil
}

type OAuthTokenOutput struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func (a *App) ExchangeOAuthCode(ctx context.Context, clientID, clientSecret, code, redirectURI, verifier string) (OAuthTokenOutput, error) {
	client, err := a.Store.GetOAuthClient(ctx, clientID)
	if errors.Is(err, sql.ErrNoRows) || client.Status != "active" {
		return OAuthTokenOutput{}, Err("invalid_client", "OAuth 客户端无效", http.StatusUnauthorized)
	}
	if err != nil {
		return OAuthTokenOutput{}, err
	}
	if client.SecretHash.Valid && !controlcrypto.ConstantTimeEqual(client.SecretHash.String, controlcrypto.HashToken(a.Cfg.TokenPepper, clientSecret)) {
		return OAuthTokenOutput{}, Err("invalid_client", "OAuth 客户端认证失败", http.StatusUnauthorized)
	}
	if code == "" || redirectURI == "" || verifier == "" {
		return OAuthTokenOutput{}, Err("invalid_grant", "授权码参数无效", http.StatusBadRequest)
	}
	obj, err := a.Store.ConsumeOAuthCode(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, code), clientID, redirectURI, time.Now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthTokenOutput{}, Err("invalid_grant", "授权码无效或已使用", http.StatusBadRequest)
	}
	if err != nil {
		return OAuthTokenOutput{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	if !obj.CodeChallenge.Valid || !controlcrypto.ConstantTimeEqual(expected, obj.CodeChallenge.String) {
		return OAuthTokenOutput{}, Err("invalid_grant", "PKCE 校验失败", http.StatusBadRequest)
	}
	now := time.Now().UTC()
	access, err := controlcrypto.NewToken(32)
	if err != nil {
		return OAuthTokenOutput{}, err
	}
	refresh, err := controlcrypto.NewToken(48)
	if err != nil {
		return OAuthTokenOutput{}, err
	}
	if err := a.Store.CreateOAuthToken(ctx, sqlite.OAuthToken{TokenHash: controlcrypto.HashToken(a.Cfg.TokenPepper, access), TokenType: "access_token", ClientID: clientID, UserID: obj.UserID, DeviceID: obj.DeviceID, Scope: obj.Scope, ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		return OAuthTokenOutput{}, err
	}
	if err := a.Store.CreateOAuthToken(ctx, sqlite.OAuthToken{TokenHash: controlcrypto.HashToken(a.Cfg.TokenPepper, refresh), TokenType: "refresh_token", ClientID: clientID, UserID: obj.UserID, DeviceID: obj.DeviceID, Scope: obj.Scope, ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now}); err != nil {
		return OAuthTokenOutput{}, err
	}
	return OAuthTokenOutput{AccessToken: access, TokenType: "Bearer", ExpiresIn: 3600, Scope: obj.Scope, RefreshToken: refresh}, nil
}

func (a *App) RefreshOAuthToken(ctx context.Context, clientID, clientSecret, rawRefresh string) (OAuthTokenOutput, error) {
	client, err := a.Store.GetOAuthClient(ctx, clientID)
	if errors.Is(err, sql.ErrNoRows) || client.Status != "active" {
		return OAuthTokenOutput{}, Err("invalid_client", "OAuth 客户端无效", http.StatusUnauthorized)
	}
	if err != nil {
		return OAuthTokenOutput{}, err
	}
	if client.SecretHash.Valid && !controlcrypto.ConstantTimeEqual(client.SecretHash.String, controlcrypto.HashToken(a.Cfg.TokenPepper, clientSecret)) {
		return OAuthTokenOutput{}, Err("invalid_client", "OAuth 客户端认证失败", http.StatusUnauthorized)
	}
	token, err := a.Store.GetOAuthToken(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, rawRefresh))
	if err != nil || token.RevokedAt != nil || !token.ExpiresAt.After(time.Now().UTC()) || token.TokenType != "refresh_token" || token.ClientID != clientID {
		return OAuthTokenOutput{}, Err("invalid_grant", "刷新令牌无效", http.StatusBadRequest)
	}
	now := time.Now().UTC()
	access, err := controlcrypto.NewToken(32)
	if err != nil {
		return OAuthTokenOutput{}, err
	}
	if err := a.Store.CreateOAuthToken(ctx, sqlite.OAuthToken{TokenHash: controlcrypto.HashToken(a.Cfg.TokenPepper, access), TokenType: "access_token", ClientID: clientID, UserID: token.UserID, DeviceID: token.DeviceID, Scope: token.Scope, ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		return OAuthTokenOutput{}, err
	}
	return OAuthTokenOutput{AccessToken: access, TokenType: "Bearer", ExpiresIn: 3600, Scope: token.Scope}, nil
}

func (a *App) AuthenticateOAuthToken(ctx context.Context, raw string) (sqlite.OAuthToken, error) {
	if strings.TrimSpace(raw) == "" {
		return sqlite.OAuthToken{}, ErrUnauthorized
	}
	token, err := a.Store.GetOAuthToken(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, raw))
	if errors.Is(err, sql.ErrNoRows) {
		return sqlite.OAuthToken{}, Err("invalid_token", "OAuth 令牌无效", http.StatusUnauthorized)
	}
	if err != nil {
		return sqlite.OAuthToken{}, err
	}
	if token.RevokedAt != nil || !token.ExpiresAt.After(time.Now().UTC()) {
		return sqlite.OAuthToken{}, Err("invalid_token", "OAuth 令牌已过期", http.StatusUnauthorized)
	}
	if token.TokenType != "access_token" {
		return sqlite.OAuthToken{}, Err("invalid_token", "令牌类型无效", http.StatusUnauthorized)
	}
	return token, nil
}

func (a *App) OAuthUserinfo(ctx context.Context, token sqlite.OAuthToken) (map[string]any, error) {
	u, err := a.Store.GetUser(ctx, token.UserID)
	if err != nil {
		return nil, err
	}
	if u.Status != "active" {
		return nil, Err("user_unavailable", "用户不可用", http.StatusForbidden)
	}
	identity, err := a.Store.GetIdentityByUser(ctx, u.ID, identityProvider)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	scopes := strings.Fields(token.Scope)
	has := func(s string) bool {
		for _, v := range scopes {
			if v == s {
				return true
			}
		}
		return false
	}
	out := map[string]any{"sub": u.ID, "name": u.DisplayName, "student_alias": identity.StudentAlias, "auth_source": "client_assertion", "scope": token.Scope}
	if has("student_id:read") {
		if sid, e := controlcrypto.Decrypt(a.Cfg.EncryptionKey, identity.StudentIDCiphertext); e == nil {
			out["student_id"] = sid
		}
	}
	if has("device:read") && token.DeviceID.Valid {
		if d, e := a.Store.GetDeviceByID(ctx, token.DeviceID.String); e == nil {
			out["device_serial"] = d.DeviceSerial
		}
	}
	return out, nil
}

func (a *App) RevokeOAuth(ctx context.Context, raw string) error {
	return a.Store.RevokeOAuthToken(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, raw), time.Now().UTC())
}

type ExternalOAuthStart struct {
	AuthorizationURL string `json:"authorization_url"`
	Provider         string `json:"provider"`
	ExpiresAt        string `json:"expires_at"`
}

func (a *App) StartExternalOAuth(ctx context.Context, p SessionPrincipal, providerID string) (ExternalOAuthStart, error) {
	provider, err := a.Store.GetOAuthProvider(ctx, providerID)
	if errors.Is(err, sql.ErrNoRows) || provider.Status != "active" {
		return ExternalOAuthStart{}, Err("provider_not_found", "OAuth Provider 不存在", http.StatusNotFound)
	}
	if err != nil {
		return ExternalOAuthStart{}, err
	}
	secret, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, provider.SecretCiphertext)
	if err != nil {
		return ExternalOAuthStart{}, err
	}
	state, err := controlcrypto.NewToken(32)
	if err != nil {
		return ExternalOAuthStart{}, err
	}
	verifier, err := controlcrypto.NewToken(32)
	if err != nil {
		return ExternalOAuthStart{}, err
	}
	redirect := a.Cfg.PublicBaseURL + "/api/v1/oauth/callback/" + url.PathEscape(providerID)
	now := time.Now().UTC()
	txID, err := controlcrypto.NewID("otx")
	if err != nil {
		return ExternalOAuthStart{}, err
	}
	cipher, err := controlcrypto.Encrypt(a.Cfg.EncryptionKey, verifier)
	if err != nil {
		return ExternalOAuthStart{}, err
	}
	if err := a.Store.CreateOAuthTransaction(ctx, sqlite.OAuthTransaction{ID: txID, UserID: p.User.ID, DeviceID: sql.NullString{String: p.Device.ID, Valid: true}, ProviderID: providerID, StateHash: controlcrypto.HashToken(a.Cfg.TokenPepper, state), CodeVerifierCiphertext: cipher, RedirectURI: redirect, ExpiresAt: now.Add(5 * time.Minute)}); err != nil {
		return ExternalOAuthStart{}, err
	}
	cfg := oauth2.Config{ClientID: provider.ClientID, ClientSecret: secret, Endpoint: oauth2.Endpoint{AuthURL: provider.AuthorizationURL, TokenURL: provider.TokenURL}, RedirectURL: redirect, Scopes: parseJSONStrings(provider.Scopes)}
	authURL := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	return ExternalOAuthStart{AuthorizationURL: authURL, Provider: providerID, ExpiresAt: now.Add(5 * time.Minute).Format(time.RFC3339)}, nil
}

func (a *App) CompleteExternalOAuth(ctx context.Context, providerID, state, code string) (sqlite.OAuthAccount, error) {
	tx, err := a.Store.ConsumeOAuthTransaction(ctx, controlcrypto.HashToken(a.Cfg.TokenPepper, state), time.Now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return sqlite.OAuthAccount{}, Err("invalid_oauth_state", "OAuth state 无效或已过期", http.StatusBadRequest)
	}
	if err != nil {
		return sqlite.OAuthAccount{}, err
	}
	if tx.ProviderID != providerID {
		return sqlite.OAuthAccount{}, Err("invalid_oauth_state", "OAuth Provider 不匹配", http.StatusBadRequest)
	}
	provider, err := a.Store.GetOAuthProvider(ctx, providerID)
	if err != nil {
		return sqlite.OAuthAccount{}, err
	}
	secret, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, provider.SecretCiphertext)
	if err != nil {
		return sqlite.OAuthAccount{}, err
	}
	verifier, err := controlcrypto.Decrypt(a.Cfg.EncryptionKey, tx.CodeVerifierCiphertext)
	if err != nil {
		return sqlite.OAuthAccount{}, err
	}
	cfg := oauth2.Config{ClientID: provider.ClientID, ClientSecret: secret, Endpoint: oauth2.Endpoint{AuthURL: provider.AuthorizationURL, TokenURL: provider.TokenURL}, RedirectURL: tx.RedirectURI, Scopes: parseJSONStrings(provider.Scopes)}
	exchangeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	oauthToken, err := cfg.Exchange(exchangeCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return sqlite.OAuthAccount{}, Err("oauth_exchange_failed", "外部 OAuth 换 token 失败", http.StatusBadGateway)
	}
	subject := ""
	displayName := ""
	if provider.UserinfoURL.Valid && provider.UserinfoURL.String != "" {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, provider.UserinfoURL.String, nil)
		if reqErr != nil {
			return sqlite.OAuthAccount{}, Err("oauth_userinfo_failed", "外部 OAuth 用户信息地址无效", http.StatusBadGateway)
		}
		req.Header.Set("Authorization", "Bearer "+oauthToken.AccessToken)
		client := &http.Client{Timeout: 10 * time.Second}
		resp, e := client.Do(req)
		if e == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				var info map[string]any
				_ = json.Unmarshal(body, &info)
				for _, key := range []string{"sub", "id", "user_id"} {
					if v, ok := info[key].(string); ok && v != "" {
						subject = v
						break
					}
				}
				for _, key := range []string{"name", "display_name", "preferred_username"} {
					if v, ok := info[key].(string); ok && v != "" {
						displayName = v
						break
					}
				}
			}
		}
		if e != nil || subject == "" {
			return sqlite.OAuthAccount{}, Err("oauth_userinfo_failed", "外部 OAuth 用户信息获取失败", http.StatusBadGateway)
		}
	}
	if subject == "" {
		subject = controlcrypto.HashBytesHex([]byte(code))
	}
	if existing, lookupErr := a.Store.GetOAuthAccountByExternal(ctx, providerID, subject); lookupErr == nil && existing.UserID != tx.UserID {
		return sqlite.OAuthAccount{}, Err("oauth_account_linked", "外部账号已绑定其他用户", http.StatusConflict)
	} else if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return sqlite.OAuthAccount{}, lookupErr
	}
	accessCipher, err := controlcrypto.Encrypt(a.Cfg.EncryptionKey, oauthToken.AccessToken)
	if err != nil {
		return sqlite.OAuthAccount{}, err
	}
	refreshCipher := ""
	if oauthToken.RefreshToken != "" {
		refreshCipher, _ = controlcrypto.Encrypt(a.Cfg.EncryptionKey, oauthToken.RefreshToken)
	}
	now := time.Now().UTC()
	account := sqlite.OAuthAccount{ID: "", UserID: tx.UserID, ProviderID: providerID, ExternalSubject: subject, DisplayName: displayName, AccessCiphertext: accessCipher, RefreshCiphertext: sql.NullString{String: refreshCipher, Valid: refreshCipher != ""}, ExpiresAt: sql.NullInt64{Valid: !oauthToken.Expiry.IsZero(), Int64: oauthToken.Expiry.UnixMilli()}, Scope: strings.Join(parseJSONStrings(provider.Scopes), " "), CreatedAt: now, UpdatedAt: now}
	id, err := controlcrypto.NewID("oac")
	if err != nil {
		return sqlite.OAuthAccount{}, err
	}
	account.ID = id
	if err := a.Store.UpsertOAuthAccount(ctx, account); err != nil {
		return sqlite.OAuthAccount{}, err
	}
	return account, nil
}

func (a *App) ListMyOAuthAccounts(ctx context.Context, p SessionPrincipal) ([]map[string]any, error) {
	accounts, err := a.Store.ListOAuthAccounts(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, map[string]any{"id": account.ID, "provider_id": account.ProviderID, "external_subject": account.ExternalSubject, "display_name": account.DisplayName, "scope": account.Scope, "expires_at": nullableMillis(account.ExpiresAt), "updated_at": account.UpdatedAt.Format(time.RFC3339)})
	}
	return out, nil
}
func nullableMillis(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return time.UnixMilli(v.Int64).UTC().Format(time.RFC3339)
}
func (a *App) DeleteMyOAuthAccount(ctx context.Context, p SessionPrincipal, providerID string) error {
	return a.Store.DeleteOAuthAccount(ctx, p.User.ID, providerID)
}

func (a *App) ClientSecretRequired(client sqlite.OAuthClient) bool { return client.SecretHash.Valid }
func (a *App) OAuthDiscovery() map[string]any {
	base := a.Cfg.PublicBaseURL
	return map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "userinfo_endpoint": base + "/oauth/userinfo", "revocation_endpoint": base + "/oauth/revoke", "jwks_uri": base + "/.well-known/jwks.json", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"openid", "profile", "student_alias", "student_id:read", "device:read"}}
}
