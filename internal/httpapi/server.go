package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"xzitpocket-control/internal/app"
)

type Server struct {
	App    *app.App
	Logger *slog.Logger
	Static http.Handler
}

func New(a *app.App, static http.Handler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if static == nil {
		static = http.NotFoundHandler()
	}
	return &Server{App: a, Static: static, Logger: logger}
}

type requestIDKey struct{}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := newRequestID()
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
	r = r.WithContext(ctx)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		s.handleAPI(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/oauth/") || r.URL.Path == "/.well-known/jwks.json" || r.URL.Path == "/.well-known/openid-configuration" {
		s.handleOAuthPublic(w, r)
		return
	}
	s.Static.ServeHTTP(w, r)
}

func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	switch {
	case r.Method == http.MethodPost && path == "/devices/register":
		s.registerDevice(w, r)
	case r.Method == http.MethodPost && path == "/auth/challenges":
		s.createChallenge(w, r)
	case r.Method == http.MethodPost && path == "/auth/assertions":
		s.assertLogin(w, r)
	case r.Method == http.MethodPost && path == "/auth/refresh":
		s.refresh(w, r)
	case r.Method == http.MethodPost && path == "/auth/revoke":
		s.revokeSession(w, r)
	case r.Method == http.MethodGet && path == "/me":
		s.me(w, r)
	case r.Method == http.MethodGet && path == "/me/devices":
		s.myDevices(w, r)
	case r.Method == http.MethodGet && path == "/me/oauth":
		s.myOAuth(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/me/oauth/") && strings.HasSuffix(path, "/start"):
		s.startExternalOAuth(w, r, segment(path, 2))
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/me/oauth/"):
		s.deleteExternalOAuth(w, r, segment(path, 2))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/oauth/callback/"):
		s.externalOAuthCallback(w, r, segment(path, 2))
	case r.Method == http.MethodPost && path == "/telemetry/events":
		s.telemetry(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/services/") && strings.HasSuffix(path, "/tokens"):
		s.issueServiceToken(w, r, segment(path, 1))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/services/") && strings.HasSuffix(path, "/introspect"):
		s.introspectServiceToken(w, r, segment(path, 1))
	case r.Method == http.MethodPost && path == "/admin/session":
		s.adminLogin(w, r)
	case r.Method == http.MethodDelete && path == "/admin/session":
		s.adminLogout(w, r)
	case r.Method == http.MethodGet && path == "/admin/metrics/overview":
		s.adminMetricsOverview(w, r)
	case r.Method == http.MethodGet && path == "/admin/metrics/series":
		s.adminMetricsSeries(w, r)
	case r.Method == http.MethodGet && path == "/admin/users":
		s.adminUsers(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/admin/users/") && !strings.HasSuffix(path, "/status"):
		s.adminUserDetail(w, r, segment(path, 2))
	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/admin/users/") && strings.HasSuffix(path, "/status"):
		s.adminUserStatus(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/devices":
		s.adminDevices(w, r)
	case r.Method == http.MethodGet && path == "/admin/risk-events":
		s.adminRisk(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/admin/risk-events/"):
		s.adminRiskAcknowledge(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/audit":
		s.adminAudit(w, r)
	case r.Method == http.MethodGet && path == "/admin/oauth-clients":
		s.adminOAuthClients(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/admin/oauth-clients/"):
		s.adminOAuthClientUpsert(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/oauth-providers":
		s.adminOAuthProviders(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/admin/oauth-providers/"):
		s.adminOAuthProviderUpsert(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/service-clients":
		s.adminServiceClients(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/admin/service-clients/"):
		s.adminServiceClientUpsert(w, r, segment(path, 2))
	default:
		writeError(w, r, app.Err("not_found", "接口不存在", http.StatusNotFound))
	}
}

func segment(path string, index int) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if index < 0 || index >= len(parts) {
		return ""
	}
	v, _ := url.PathUnescape(parts[index])
	return v
}

func decodeJSON(r *http.Request, dst any, max int64) error {
	if max <= 0 {
		max = 1 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > max {
		return errors.New("request body too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}
func decodeJSONLoose(r *http.Request, dst any, max int64) error {
	if max <= 0 {
		max = 1 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > max {
		return errors.New("request body too large")
	}
	return json.NewDecoder(bytes.NewReader(raw)).Decode(dst)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "服务器内部错误"
	var apiErr *app.APIError
	if errors.As(err, &apiErr) {
		status = apiErr.Status
		code = apiErr.Code
		message = apiErr.Message
	}
	if status >= 500 {
		rctx := r.Context()
		_ = rctx
		slog.Default().Error("request failed", "error", err)
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}, "request_id": requestID(r)})
}
func requestID(r *http.Request) string {
	if v, ok := r.Context().Value(requestIDKey{}).(string); ok {
		return v
	}
	return "req_unknown"
}
func bearer(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(v) >= 7 && strings.EqualFold(v[:7], "Bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}
func deviceToken(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(v) >= 7 && strings.EqualFold(v[:7], "Device ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}
func parseIntQuery(r *http.Request, key string, def int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return v
}
func adminToken(r *http.Request) string {
	if v := bearer(r); v != "" {
		return v
	}
	if c, err := r.Cookie("control_admin"); err == nil {
		return c.Value
	}
	return ""
}

func checkAdminCSRF(w http.ResponseWriter, r *http.Request) bool {
	// Bearer-authenticated clients do not use the browser session cookie.
	if bearer(r) != "" {
		return true
	}
	adminCookie, adminErr := r.Cookie("control_admin")
	csrfCookie, csrfErr := r.Cookie("control_csrf")
	if adminErr != nil || csrfErr != nil || adminCookie.Value == "" || csrfCookie.Value == "" || r.Header.Get("X-CSRF-Token") != csrfCookie.Value {
		writeError(w, r, app.Err("csrf_failed", "CSRF 校验失败", http.StatusForbidden))
		return false
	}
	return true
}
func sessionToken(r *http.Request) string {
	if v := bearer(r); v != "" {
		return v
	}
	if c, err := r.Cookie("control_access"); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) authDevice(w http.ResponseWriter, r *http.Request) (app.DevicePrincipal, bool) {
	p, err := s.App.AuthenticateDevice(r.Context(), deviceToken(r))
	if err != nil {
		writeError(w, r, err)
		return p, false
	}
	return p, true
}
func (s *Server) authSession(w http.ResponseWriter, r *http.Request) (app.SessionPrincipal, bool) {
	p, err := s.App.AuthenticateSession(r.Context(), sessionToken(r))
	if err != nil {
		writeError(w, r, err)
		return p, false
	}
	return p, true
}
func (s *Server) authAdmin(w http.ResponseWriter, r *http.Request, roles ...string) (app.AdminPrincipal, bool) {
	p, err := s.App.AuthenticateAdmin(r.Context(), adminToken(r))
	if err != nil {
		writeError(w, r, err)
		return p, false
	}
	if len(roles) > 0 {
		allowed := false
		for _, role := range roles {
			if p.Admin.Role == role || p.Admin.Role == "admin" {
				allowed = true
			}
		}
		if !allowed {
			writeError(w, r, app.ErrForbidden)
			return p, false
		}
	}
	return p, true
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	var in app.RegisterDeviceInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.RegisterDevice(r.Context(), in, r.Header.Get("X-Device-Signature"))
	if err != nil {
		if out.DeviceSerial != "" {
			writeJSON(w, 409, map[string]any{"error": map[string]string{"code": "device_already_registered", "message": "该安装已注册，请使用已保存的设备令牌"}, "device_id": out.DeviceID, "device_serial": out.DeviceSerial, "request_id": requestID(r)})
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) createChallenge(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authDevice(w, r)
	if !ok {
		return
	}
	out, err := s.App.CreateChallenge(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) assertLogin(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authDevice(w, r)
	if !ok {
		return
	}
	var in app.AssertionInput
	if err := decodeJSON(r, &in, 64<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.AssertLogin(r.Context(), p, in, r.Header.Get("X-Device-Signature"), r.Header.Get("X-Installation-ID"), r.Header.Get("X-Device-Signed-At"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(r, &in, 32<<10); err != nil && bearer(r) == "" {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if in.RefreshToken == "" {
		in.RefreshToken = bearer(r)
	}
	out, err := s.App.RefreshSession(r.Context(), in.RefreshToken, r.Header.Get("X-Device-Signature"), r.Header.Get("X-Installation-ID"), r.Header.Get("X-Device-Signed-At"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	if err := s.App.Store.RevokeSession(r.Context(), p.Session.ID, time.Now().UTC()); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	out, err := s.App.CurrentUser(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) myDevices(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	out, err := s.App.UserDevices(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
func (s *Server) myOAuth(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	out, err := s.App.ListMyOAuthAccounts(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
func (s *Server) startExternalOAuth(w http.ResponseWriter, r *http.Request, provider string) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	out, err := s.App.StartExternalOAuth(r.Context(), p, provider)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) deleteExternalOAuth(w http.ResponseWriter, r *http.Request, provider string) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	if err := s.App.DeleteMyOAuthAccount(r.Context(), p, provider); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
func (s *Server) externalOAuthCallback(w http.ResponseWriter, r *http.Request, provider string) {
	if r.URL.Query().Get("error") != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": r.URL.Query().Get("error")})
		return
	}
	account, err := s.App.CompleteExternalOAuth(r.Context(), provider, r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "provider_id": account.ProviderID, "display_name": account.DisplayName})
}
func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	var session *app.SessionPrincipal
	var device app.DevicePrincipal
	if token := deviceToken(r); token != "" {
		p, ok := s.authDevice(w, r)
		if !ok {
			return
		}
		device = p
	} else {
		p, ok := s.authSession(w, r)
		if !ok {
			return
		}
		session = &p
		device = app.DevicePrincipal{Device: p.Device, Token: p.Token}
	}
	if err := decodeJSONLoose(r, &raw, 256<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	var events []app.TelemetryEventInput
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &events); err != nil {
			writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
			return
		}
	} else {
		var wrapper struct {
			Events []app.TelemetryEventInput `json:"events"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
			return
		}
		events = wrapper.Events
	}
	out, err := s.App.InsertTelemetry(r.Context(), device, session, events)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *Server) issueServiceToken(w http.ResponseWriter, r *http.Request, service string) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	var in app.ServiceTokenInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.IssueServiceToken(r.Context(), p, service, in, r.Header.Get("X-Device-Signature"), r.Header.Get("X-Installation-ID"), r.Header.Get("X-Device-Signed-At"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) introspectServiceToken(w http.ResponseWriter, r *http.Request, service string) {
	raw := bearer(r)
	if raw == "" {
		writeError(w, r, app.ErrUnauthorized)
		return
	}
	out, err := s.App.IntrospectServiceToken(service, raw)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	token, p, err := s.App.AdminLogin(r.Context(), in.Username, in.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	csrf := newRequestID()
	secure := strings.HasPrefix(strings.ToLower(s.App.Cfg.PublicBaseURL), "https://")
	http.SetCookie(w, &http.Cookie{Name: "control_admin", Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	http.SetCookie(w, &http.Cookie{Name: "control_csrf", Value: csrf, Path: "/", HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token, "expires_in": 43200, "admin": map[string]any{"id": p.Admin.ID, "username": p.Admin.Username, "role": p.Admin.Role}, "csrf_token": csrf})
}
func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok {
		return
	}
	if !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.RevokeAdmin(r.Context(), p); err != nil {
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "control_admin", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: "control_csrf", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

func queryPage(r *http.Request) (int, int) {
	return parseIntQuery(r, "limit", 50), parseIntQuery(r, "offset", 0)
}
func (s *Server) adminMetricsOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	out, err := s.App.MetricsOverview(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) adminMetricsSeries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	days := parseIntQuery(r, "days", 30)
	out, err := s.App.MetricsSeries(r.Context(), days)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListUsers(r.Context(), limit, offset, r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}
func (s *Server) adminUserDetail(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	out, err := s.App.GetUserDetail(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) adminUserStatus(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r, "admin")
	if !ok {
		return
	}
	if !checkAdminCSRF(w, r) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if err := s.App.SetUserStatus(r.Context(), id, in.Status, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}
func (s *Server) adminDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListDevices(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}
func (s *Server) adminRisk(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	limit, offset := queryPage(r)
	var ack *bool
	if v := r.URL.Query().Get("acknowledged"); v != "" {
		b := v == "true"
		ack = &b
	}
	items, err := s.App.ListRisk(r.Context(), limit, offset, ack)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) adminRiskAcknowledge(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	if !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.AcknowledgeRisk(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true})
}
func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "analyst"); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, err := s.App.ListAudit(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminOAuthClients(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "admin"); !ok {
		return
	}
	items, err := s.App.ListOAuthClients(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, c := range items {
		out = append(out, map[string]any{"client_id": c.ClientID, "client_name": c.ClientName, "redirect_uris": parseJSON(c.RedirectURIs), "scopes": parseJSON(c.Scopes), "status": c.Status, "has_secret": c.SecretHash.Valid, "created_at": c.CreatedAt.Format(time.RFC3339), "updated_at": c.UpdatedAt.Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
func (s *Server) adminOAuthClientUpsert(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r, "admin")
	if !ok {
		return
	}
	if !checkAdminCSRF(w, r) {
		return
	}
	var in app.OAuthClientInput
	if err := decodeJSON(r, &in, 64<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if in.ClientID == "" {
		in.ClientID = id
	}
	c, err := s.App.UpsertOAuthClient(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = s.App.Store.AddAudit(r.Context(), p.Admin.ID, "oauth_client_upsert", "oauth_client", c.ClientID, "{}", time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"client_id": c.ClientID, "client_name": c.ClientName, "redirect_uris": parseJSON(c.RedirectURIs), "scopes": parseJSON(c.Scopes), "status": c.Status, "has_secret": c.SecretHash.Valid})
}
func (s *Server) adminOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "admin"); !ok {
		return
	}
	items, err := s.App.ListOAuthProviders(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, p := range items {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "authorization_url": p.AuthorizationURL, "token_url": p.TokenURL, "userinfo_url": p.UserinfoURL.String, "client_id": p.ClientID, "scopes": parseJSON(p.Scopes), "status": p.Status, "configured": p.SecretCiphertext != "", "updated_at": p.UpdatedAt.Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
func (s *Server) adminOAuthProviderUpsert(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r, "admin")
	if !ok {
		return
	}
	if !checkAdminCSRF(w, r) {
		return
	}
	var in app.OAuthProviderInput
	if err := decodeJSON(r, &in, 64<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if in.ID == "" {
		in.ID = id
	}
	provider, err := s.App.UpsertOAuthProvider(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = s.App.Store.AddAudit(r.Context(), p.Admin.ID, "oauth_provider_upsert", "oauth_provider", provider.ID, "{}", time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"id": provider.ID, "name": provider.Name, "status": provider.Status, "configured": provider.SecretCiphertext != ""})
}
func (s *Server) adminServiceClients(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r, "admin"); !ok {
		return
	}
	items, err := s.App.ListServiceClients(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, c := range items {
		out = append(out, map[string]any{"id": c.ID, "audience": c.Audience, "scopes": parseJSON(c.Scopes), "status": c.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
func (s *Server) adminServiceClientUpsert(w http.ResponseWriter, r *http.Request, audience string) {
	p, ok := s.authAdmin(w, r, "admin")
	if !ok {
		return
	}
	if !checkAdminCSRF(w, r) {
		return
	}
	var in app.ServiceClientInput
	if err := decodeJSON(r, &in, 64<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if in.Audience == "" {
		in.Audience = audience
	}
	client, err := s.App.UpsertServiceClient(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = s.App.Store.AddAudit(r.Context(), p.Admin.ID, "service_client_upsert", "service_client", client.Audience, "{}", time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"id": client.ID, "audience": client.Audience, "scopes": parseJSON(client.Scopes), "status": client.Status, "configured": client.SecretCiphertext.Valid})
}
func parseJSON(raw string) any {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return []any{}
	}
	return v
}

func (s *Server) handleOAuthPublic(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/jwks.json":
		writeJSON(w, http.StatusOK, s.App.JWKS())
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/openid-configuration":
		writeJSON(w, http.StatusOK, s.App.OAuthDiscovery())
	case r.Method == http.MethodGet && r.URL.Path == "/oauth/authorize":
		s.oauthAuthorize(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/token":
		s.oauthToken(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/oauth/userinfo":
		s.oauthUserinfo(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/userinfo":
		s.oauthUserinfo(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/revoke":
		s.oauthRevoke(w, r)
	default:
		writeError(w, r, app.Err("not_found", "接口不存在", http.StatusNotFound))
	}
}

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	token := sessionToken(r)
	if token == "" {
		token = r.URL.Query().Get("access_token")
	}
	p, err := s.App.AuthenticateSession(r.Context(), token)
	if err != nil {
		writeError(w, r, err)
		return
	}
	redirect, err := s.App.Authorize(r.Context(), p, app.AuthorizeInput{ClientID: r.URL.Query().Get("client_id"), RedirectURI: r.URL.Query().Get("redirect_uri"), ResponseType: r.URL.Query().Get("response_type"), Scope: r.URL.Query().Get("scope"), State: r.URL.Query().Get("state"), CodeChallenge: r.URL.Query().Get("code_challenge"), CodeChallengeMethod: r.URL.Query().Get("code_challenge_method")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}
func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, r, app.Err("invalid_request", "请求格式无效", http.StatusBadRequest))
		return
	}
	clientID, secret := r.Form.Get("client_id"), r.Form.Get("client_secret")
	if clientID == "" {
		if id, sec, ok := r.BasicAuth(); ok {
			clientID, secret = id, sec
		}
	}
	if r.Form.Get("grant_type") != "authorization_code" {
		writeError(w, r, app.Err("unsupported_grant_type", "仅支持 authorization_code", http.StatusBadRequest))
		return
	}
	out, err := s.App.ExchangeOAuthCode(r.Context(), clientID, secret, r.Form.Get("code"), r.Form.Get("redirect_uri"), r.Form.Get("code_verifier"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) oauthUserinfo(w http.ResponseWriter, r *http.Request) {
	token, err := s.App.AuthenticateOAuthToken(r.Context(), bearer(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.App.OAuthUserinfo(r.Context(), token)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, r, app.Err("invalid_request", "请求格式无效", http.StatusBadRequest))
		return
	}
	if r.Form.Get("token") == "" {
		writeError(w, r, app.Err("invalid_request", "缺少 token", http.StatusBadRequest))
		return
	}
	_ = s.App.RevokeOAuth(r.Context(), r.Form.Get("token"))
	w.WriteHeader(http.StatusOK)
}
