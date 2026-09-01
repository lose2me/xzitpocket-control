package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	controlcrypto "xzitpocket-control/internal/crypto"
)

type ServiceTokenInput struct {
	Scope []string `json:"scope"`
}
type ServiceTokenOutput struct {
	Token        string   `json:"token"`
	TokenType    string   `json:"token_type"`
	Service      string   `json:"service"`
	Scope        []string `json:"scope"`
	DeviceSerial string   `json:"device_serial"`
	ExpiresAt    string   `json:"expires_at"`
}

func normalizeScopes(scopes []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || len(scope) > 128 || strings.ContainsAny(scope, " \t\r\n") {
			return nil, errors.New("invalid scope")
		}
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	sort.Strings(out)
	return out, nil
}

func decodeStringList(raw string) []string {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil
	}
	return values
}

func containsAll(allowed []string, requested []string) bool {
	set := map[string]bool{}
	for _, s := range allowed {
		set[s] = true
	}
	for _, s := range requested {
		if !set[s] {
			return false
		}
	}
	return true
}

func (a *App) IssueServiceToken(ctx context.Context, p SessionPrincipal, service string, in ServiceTokenInput, signature, installationID, signedAt string) (ServiceTokenOutput, error) {
	service = strings.TrimSpace(service)
	if service == "" || len(service) > 128 || strings.ContainsAny(service, "/\\\r\n") {
		return ServiceTokenOutput{}, Err("invalid_service", "服务名称无效", http.StatusBadRequest)
	}
	scopes, err := normalizeScopes(in.Scope)
	if err != nil || len(scopes) == 0 {
		return ServiceTokenOutput{}, Err("invalid_scope", "scope 无效", http.StatusBadRequest)
	}
	config, err := a.Store.GetServiceClientByAudience(ctx, service)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceTokenOutput{}, Err("service_not_configured", "服务未配置", http.StatusNotFound)
	}
	if err != nil {
		return ServiceTokenOutput{}, err
	}
	if config.Status != "active" || !containsAll(decodeStringList(config.Scopes), scopes) {
		return ServiceTokenOutput{}, Err("scope_not_allowed", "请求的 scope 未被允许", http.StatusForbidden)
	}
	if installationID != "" && installationID != p.Device.Installation {
		return ServiceTokenOutput{}, Err("installation_mismatch", "安装标识不匹配", http.StatusUnauthorized)
	}
	if signedAt == "" {
		return ServiceTokenOutput{}, Err("signature_required", "需要设备签名", http.StatusUnauthorized)
	}
	t, err := time.Parse(time.RFC3339, signedAt)
	if err != nil || absDuration(time.Since(t)) > 10*time.Minute {
		return ServiceTokenOutput{}, Err("invalid_timestamp", "设备签名时间无效", http.StatusBadRequest)
	}
	pub, err := controlcrypto.ParseP256PublicKey(p.Device.PublicKey)
	if err != nil {
		return ServiceTokenOutput{}, err
	}
	accessSum := sha256.Sum256([]byte(p.Token))
	accessDigest := strings.ToLower(hexEncode(accessSum[:]))
	message, err := controlcrypto.CanonicalLines("xzitpocket-control-service-token", accessDigest, service, p.Device.DeviceSerial, strings.Join(scopes, " "), signedAt)
	if err != nil || !controlcrypto.VerifyP256Signature(pub, message, signature) {
		return ServiceTokenOutput{}, Err("invalid_device_signature", "设备签名无效", http.StatusUnauthorized)
	}
	now := time.Now().UTC()
	expiry := now.Add(10 * time.Minute)
	identity, _ := a.Store.GetIdentityByUser(ctx, p.User.ID, identityProvider)
	claims := jwt.MapClaims{"iss": a.Cfg.PublicBaseURL, "aud": service, "sub": p.User.ID, "student_alias": identity.StudentAlias, "device_serial": p.Device.DeviceSerial, "scope": strings.Join(scopes, " "), "iat": now.Unix(), "exp": expiry.Unix()}
	jti, err := controlcrypto.NewID("jti")
	if err != nil {
		return ServiceTokenOutput{}, err
	}
	claims["jti"] = jti
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = a.ServiceKeyID
	signed, err := token.SignedString(a.ServicePriv)
	if err != nil {
		return ServiceTokenOutput{}, err
	}
	return ServiceTokenOutput{Token: signed, TokenType: "Bearer", Service: service, Scope: scopes, DeviceSerial: p.Device.DeviceSerial, ExpiresAt: expiry.Format(time.RFC3339)}, nil
}

func hexEncode(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&15]
	}
	return string(out)
}

func (a *App) JWKS() map[string]any {
	return map[string]any{"keys": []any{map[string]any{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(a.ServicePub), "use": "sig", "alg": "EdDSA", "kid": a.ServiceKeyID}}}
}

type Introspection struct {
	Active       bool   `json:"active"`
	Sub          string `json:"sub,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Aud          any    `json:"aud,omitempty"`
	Exp          int64  `json:"exp,omitempty"`
	Iat          int64  `json:"iat,omitempty"`
	DeviceSerial string `json:"device_serial,omitempty"`
}

func (a *App) IntrospectServiceToken(service, raw string) (Introspection, error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithAudience(service), jwt.WithIssuer(a.Cfg.PublicBaseURL))
	token, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Header["kid"] != a.ServiceKeyID {
			return nil, errors.New("unknown signing key")
		}
		return a.ServicePub, nil
	})
	if err != nil || !token.Valid {
		return Introspection{Active: false}, nil
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Introspection{Active: false}, nil
	}
	result := Introspection{Active: true}
	if v, ok := claims["sub"].(string); ok {
		result.Sub = v
	}
	if v, ok := claims["scope"].(string); ok {
		result.Scope = v
	}
	if v, ok := claims["device_serial"].(string); ok {
		result.DeviceSerial = v
	}
	if v, ok := claims["client_id"].(string); ok {
		result.ClientID = v
	}
	if v, ok := claims["exp"].(float64); ok {
		result.Exp = int64(v)
	}
	if v, ok := claims["iat"].(float64); ok {
		result.Iat = int64(v)
	}
	result.Aud = claims["aud"]
	return result, nil
}
