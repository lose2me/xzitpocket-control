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
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"xzitpocket-control/internal/app"
	controlcrypto "xzitpocket-control/internal/crypto"
)

type Server struct {
	App    *app.App
	Logger *slog.Logger
	Static http.Handler
	rateMu sync.Mutex
	rates  map[string]rateWindow
}

const apiPrefix = "/api/v1"

type rateWindow struct {
	started time.Time
	count   int
}

func New(a *app.App, static http.Handler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if static == nil {
		static = http.NotFoundHandler()
	}
	return &Server{App: a, Static: static, Logger: logger, rates: make(map[string]rateWindow)}
}

type requestIDKey struct{}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := newRequestID()
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
	r = r.WithContext(ctx)
	if r.Method == http.MethodOptions {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/healthz" {
		w.Header().Set("Cache-Control", "no-store")
		if err := s.App.Store.DB.PingContext(r.Context()); err != nil {
			writeError(w, r, app.Err("database_unavailable", "数据库不可用", http.StatusServiceUnavailable))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
		return
	}
	if r.URL.Path == apiPrefix || strings.HasPrefix(r.URL.Path, apiPrefix+"/") {
		w.Header().Set("Cache-Control", "no-store")
		s.handleAPI(w, r)
		return
	}
	// Static assets: cache immutable vendor libraries long; other UI files are
	// revalidated so admin updates propagate without a hard refresh.
	if strings.HasPrefix(r.URL.Path, "/vendor/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
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
	path := strings.TrimPrefix(r.URL.Path, apiPrefix)
	switch {
	case r.Method == http.MethodPost && path == "/devices/register":
		if !s.allow(r, "device-register", 1000, time.Hour) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.registerDevice(w, r)
	case r.Method == http.MethodPost && path == "/auth/challenges":
		if !s.allow(r, "challenge", 120, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.createChallenge(w, r)
	case r.Method == http.MethodPost && path == "/auth/assertions":
		if !s.allow(r, "assertion", 120, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.assertLogin(w, r)
	case r.Method == http.MethodPost && path == "/auth/refresh":
		s.refresh(w, r)
	case r.Method == http.MethodPost && path == "/auth/revoke":
		s.revokeSession(w, r)
	case r.Method == http.MethodGet && path == "/me":
		s.me(w, r)
	case r.Method == http.MethodGet && path == "/me/devices":
		s.myDevices(w, r)
	case r.Method == http.MethodPost && path == "/telemetry/events":
		s.telemetry(w, r)
	case r.Method == http.MethodPost && path == "/error-reports":
		if !s.allow(r, "error-report", 120, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.errorReport(w, r)
	case r.Method == http.MethodGet && path == "/app/release":
		s.appRelease(w, r)
	case r.Method == http.MethodGet && path == "/question-banks":
		s.questionBanks(w, r)
	case r.Method == http.MethodGet && pathMatchesID(path, "question-banks"):
		s.questionBank(w, r, segment(path, 1))
	case r.Method == http.MethodPost && path == "/library/cdks/redeem":
		if !s.allow(r, "library-cdk-redeem", 60, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.redeemLibraryCDK(w, r)
	case r.Method == http.MethodPost && path == "/admin/session":
		if !s.allow(r, "admin-login", 20, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.adminLogin(w, r)
	case r.Method == http.MethodDelete && path == "/admin/session":
		s.adminLogout(w, r)
	case r.Method == http.MethodGet && path == "/admin/metrics/overview":
		s.adminMetricsOverview(w, r)
	case r.Method == http.MethodGet && path == "/admin/metrics/series":
		s.adminMetricsSeries(w, r)
	case r.Method == http.MethodGet && path == "/admin/metrics/breakdown":
		s.adminMetricsBreakdown(w, r)
	case r.Method == http.MethodGet && path == "/admin/users":
		s.adminUsers(w, r)
	case r.Method == http.MethodGet && pathMatchesID(path, "admin", "users"):
		s.adminUserDetail(w, r, segment(path, 2))
	case r.Method == http.MethodPatch && pathMatchesIDSuffix(path, "status", "admin", "users"):
		s.adminUserStatus(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/devices":
		s.adminDevices(w, r)
	case r.Method == http.MethodGet && path == "/admin/risk-events":
		s.adminRisk(w, r)
	case r.Method == http.MethodPatch && pathMatchesID(path, "admin", "risk-events"):
		s.adminRiskAcknowledge(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/audit":
		s.adminAudit(w, r)
	case r.Method == http.MethodGet && path == "/admin/error-reports":
		s.adminErrorReports(w, r)
	case r.Method == http.MethodGet && path == "/admin/app/release":
		s.adminAppRelease(w, r)
	case r.Method == http.MethodPut && path == "/admin/app/release":
		s.adminAppReleaseUpdate(w, r)
	case r.Method == http.MethodGet && path == "/admin/question-banks":
		s.adminQuestionBanks(w, r)
	case r.Method == http.MethodPost && path == "/admin/question-banks":
		s.adminQuestionBankCreate(w, r)
	case r.Method == http.MethodGet && pathMatchesID(path, "admin", "question-banks"):
		s.adminQuestionBankDetail(w, r, segment(path, 2))
	case r.Method == http.MethodPut && pathMatchesID(path, "admin", "question-banks"):
		s.adminQuestionBankUpdate(w, r, segment(path, 2))
	case r.Method == http.MethodPatch && pathMatchesIDSuffix(path, "status", "admin", "question-banks"):
		s.adminQuestionBankStatus(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/library-cdks":
		s.adminLibraryCDKs(w, r)
	case r.Method == http.MethodPost && path == "/admin/library-cdks":
		s.adminLibraryCDKCreate(w, r)
	case r.Method == http.MethodPatch && pathMatchesID(path, "admin", "library-cdks"):
		s.adminLibraryCDKStatus(w, r, segment(path, 2))
	default:
		writeError(w, r, app.Err("not_found", "接口不存在", http.StatusNotFound))
	}
}

func (s *Server) allow(r *http.Request, bucket string, limit int, window time.Duration) bool {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	key := bucket + "|" + remote
	now := time.Now()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if s.rates == nil {
		s.rates = make(map[string]rateWindow)
	}
	entry := s.rates[key]
	if entry.started.IsZero() || now.Sub(entry.started) >= window {
		s.rates[key] = rateWindow{started: now, count: 1}
		return true
	}
	if entry.count >= limit {
		return false
	}
	entry.count++
	s.rates[key] = entry
	if len(s.rates) > 10000 {
		for k, v := range s.rates {
			if now.Sub(v.started) >= window {
				delete(s.rates, k)
			}
		}
	}
	return true
}

func segment(path string, index int) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if index < 0 || index >= len(parts) {
		return ""
	}
	return parts[index]
}

func pathMatchesID(path string, prefix ...string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != len(prefix)+1 || parts[len(parts)-1] == "" {
		return false
	}
	for i, want := range prefix {
		if parts[i] != want {
			return false
		}
	}
	return true
}

func pathMatchesIDSuffix(path, suffix string, prefix ...string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != len(prefix)+2 || parts[len(prefix)] == "" || parts[len(prefix)+1] != suffix {
		return false
	}
	for i, want := range prefix {
		if parts[i] != want {
			return false
		}
	}
	return true
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
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="xzitpocket-control"`)
	}
	if status >= 500 {
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
func (s *Server) authAdmin(w http.ResponseWriter, r *http.Request) (app.AdminPrincipal, bool) {
	p, err := s.App.AuthenticateAdmin(r.Context(), adminToken(r))
	if err != nil {
		writeError(w, r, err)
		return p, false
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
		s.App.ObserveLoginAttempt(r.Context(), p.Device.ID, s.sourceIPHash(r), "malformed_request")
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.AssertLogin(r.Context(), p, in, r.Header.Get("X-Device-Signature"), r.Header.Get("X-Installation-ID"), r.Header.Get("X-Device-Signed-At"), s.sourceIPHash(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.setAccessCookie(w, out.AccessToken, 15*time.Minute)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) sourceIPHash(r *http.Request) string {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = host
	}
	if ip == "" {
		return ""
	}
	return controlcrypto.HMACHex(s.App.Cfg.IDPepper, ip)
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
	s.setAccessCookie(w, out.AccessToken, 15*time.Minute)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setAccessCookie(w http.ResponseWriter, token string, lifetime time.Duration) {
	secure := strings.HasPrefix(strings.ToLower(s.App.Cfg.PublicBaseURL), "https://")
	http.SetCookie(w, &http.Cookie{Name: "control_access", Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(lifetime.Seconds())})
}
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	if err := s.App.RevokeSession(r.Context(), p); err != nil {
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "control_access", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
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
func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
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
	var events []app.TelemetryEventInput
	if err := decodeJSON(r, &events, 256<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.InsertTelemetry(r.Context(), device, session, events)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	token, _, err := s.App.AdminLogin(r.Context(), in.Key)
	if err != nil {
		writeError(w, r, err)
		return
	}
	csrf := newRequestID()
	secure := strings.HasPrefix(strings.ToLower(s.App.Cfg.PublicBaseURL), "https://")
	http.SetCookie(w, &http.Cookie{Name: "control_admin", Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	http.SetCookie(w, &http.Cookie{Name: "control_csrf", Value: csrf, Path: "/", HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token, "expires_in": 43200, "csrf_token": csrf})
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
	if _, ok := s.authAdmin(w, r); !ok {
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
	if _, ok := s.authAdmin(w, r); !ok {
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
func (s *Server) adminMetricsBreakdown(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	out, err := s.App.MetricsBreakdown(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
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
	if _, ok := s.authAdmin(w, r); !ok {
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
	p, ok := s.authAdmin(w, r)
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
	if _, ok := s.authAdmin(w, r); !ok {
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
	if _, ok := s.authAdmin(w, r); !ok {
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
	p, ok := s.authAdmin(w, r)
	if !ok {
		return
	}
	if !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.AcknowledgeRisk(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	_ = s.App.Store.AddAudit(r.Context(), p.Admin.ID, "risk_acknowledge", "risk_event", id, "{}", time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true})
}
func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
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
