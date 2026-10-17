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

const maxRateEntries = 10000

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
	if strings.HasPrefix(path, "/admin/") && path != "/admin/session" {
		principal, ok := s.authAdmin(w, r)
		if !ok {
			return
		}
		page, write := adminPageFor(path, r.Method)
		if page != "" && !principal.Admin.Allows(page, write) {
			writeError(w, r, app.Err("forbidden", "无权访问", http.StatusForbidden))
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), adminPrincipalKey{}, principal))
	}
	switch {
	case r.Method == http.MethodPost && path == "/devices/register":
		if !s.allow(r, "device-register", 1000, time.Hour) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.registerDevice(w, r)
	case r.Method == http.MethodPost && path == "/auth/login-eligibility":
		if !s.allow(r, "login-eligibility", 120, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.loginEligibility(w, r)
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
	case r.Method == http.MethodPost && path == "/courses":
		if !s.allow(r, "course-upload", 120, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.courseUpload(w, r)
	case r.Method == http.MethodPost && path == "/share-codes":
		if !s.allow(r, "share-code-create", 30, time.Hour) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.createShareCode(w, r)
	case r.Method == http.MethodGet && pathMatchesID(path, "share-codes"):
		if !s.allow(r, "share-code-read", 120, time.Minute) {
			writeError(w, r, app.Err("rate_limited", "请求过于频繁", http.StatusTooManyRequests))
			return
		}
		s.readShareCode(w, r, segment(path, 1))
	case r.Method == http.MethodGet && path == "/app/release":
		s.appRelease(w, r)
	case r.Method == http.MethodGet && path == "/app/release-test":
		s.appReleaseTest(w, r)
	case r.Method == http.MethodGet && path == "/config/versions":
		s.configVersions(w, r)
	case r.Method == http.MethodGet && path == "/school-calendar":
		s.schoolCalendar(w, r)
	case r.Method == http.MethodGet && path == "/resource-center/courses":
		s.userResourceCourses(w, r)
	case r.Method == http.MethodGet && path == "/resources":
		s.userCourseResources(w, r)
	case r.Method == http.MethodGet && path == "/textbooks":
		s.userCourseTextbooks(w, r)
	case r.Method == http.MethodGet && pathMatchesIDSuffix(path, "url", "resources"):
		s.userResourceURL(w, r, segment(path, 1))
	case r.Method == http.MethodGet && pathMatchesIDSuffix(path, "url", "textbooks"):
		s.userTextbookURL(w, r, segment(path, 1))
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
	case r.Method == http.MethodGet && path == "/admin/me":
		s.adminMe(w, r)
	case r.Method == http.MethodGet && path == "/admin/admins":
		s.adminAdmins(w, r)
	case r.Method == http.MethodPost && path == "/admin/admins":
		s.adminAdminCreate(w, r)
	case r.Method == http.MethodPut && pathMatchesID(path, "admin", "admins"):
		s.adminAdminUpdate(w, r, segment(path, 2))
	case r.Method == http.MethodDelete && pathMatchesID(path, "admin", "admins"):
		s.adminAdminDelete(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/metrics/overview":
		s.adminMetricsOverview(w, r)
	case r.Method == http.MethodGet && path == "/admin/metrics/series":
		s.adminMetricsSeries(w, r)
	case r.Method == http.MethodGet && path == "/admin/metrics/breakdown":
		s.adminMetricsBreakdown(w, r)
	case r.Method == http.MethodGet && path == "/admin/cdn/stats":
		s.adminCDNStats(w, r)
	case r.Method == http.MethodGet && path == "/admin/host/stats":
		s.adminHostStats(w, r)
	case r.Method == http.MethodGet && path == "/admin/buckets/stats":
		s.adminBucketStats(w, r)
	case r.Method == http.MethodGet && path == "/admin/overview-display":
		s.adminOverviewDisplay(w, r)
	case r.Method == http.MethodPut && path == "/admin/overview-display":
		s.adminOverviewDisplayUpdate(w, r)
	case r.Method == http.MethodGet && path == "/admin/users":
		s.adminUsers(w, r)
	case r.Method == http.MethodGet && pathMatchesID(path, "admin", "users"):
		s.adminUserDetail(w, r, segment(path, 2))
	case r.Method == http.MethodPatch && pathMatchesIDSuffix(path, "status", "admin", "users"):
		s.adminUserStatus(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/devices":
		s.adminDevices(w, r)
	case r.Method == http.MethodPatch && pathMatchesIDSuffix(path, "status", "admin", "devices"):
		s.adminDeviceStatus(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/risk-events":
		s.adminRisk(w, r)
	case r.Method == http.MethodDelete && path == "/admin/risk-events":
		s.adminRiskClear(w, r)
	case r.Method == http.MethodPatch && pathMatchesID(path, "admin", "risk-events"):
		s.adminRiskAcknowledge(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/audit":
		s.adminAudit(w, r)
	case r.Method == http.MethodGet && path == "/admin/error-reports":
		s.adminErrorReports(w, r)
	case r.Method == http.MethodDelete && path == "/admin/error-reports":
		s.adminErrorReportsClear(w, r)
	case r.Method == http.MethodPatch && pathMatchesID(path, "admin", "error-reports"):
		s.adminErrorReportStudentIgnored(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/app/release":
		s.adminAppRelease(w, r)
	case r.Method == http.MethodPut && path == "/admin/app/release":
		s.adminAppReleaseUpdate(w, r)
	case r.Method == http.MethodGet && path == "/admin/app/release-test":
		s.adminAppReleaseTest(w, r)
	case r.Method == http.MethodPut && path == "/admin/app/release-test":
		s.adminAppReleaseTestUpdate(w, r)
	case r.Method == http.MethodGet && path == "/admin/school-calendar":
		s.adminSchoolCalendar(w, r)
	case r.Method == http.MethodPut && path == "/admin/school-calendar":
		s.adminSchoolCalendarUpdate(w, r)
	case r.Method == http.MethodGet && path == "/admin/question-banks":
		s.adminQuestionBanks(w, r)
	case r.Method == http.MethodPost && path == "/admin/question-banks":
		s.adminQuestionBankCreate(w, r)
	case r.Method == http.MethodGet && pathMatchesID(path, "admin", "question-banks"):
		s.adminQuestionBankDetail(w, r, segment(path, 2))
	case r.Method == http.MethodPut && pathMatchesID(path, "admin", "question-banks"):
		s.adminQuestionBankUpdate(w, r, segment(path, 2))
	case r.Method == http.MethodDelete && pathMatchesID(path, "admin", "question-banks"):
		s.adminQuestionBankDelete(w, r, segment(path, 2))
	case r.Method == http.MethodPatch && pathMatchesIDSuffix(path, "status", "admin", "question-banks"):
		s.adminQuestionBankStatus(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/library-cdks":
		s.adminLibraryCDKs(w, r)
	case r.Method == http.MethodPost && path == "/admin/library-cdks":
		s.adminLibraryCDKCreate(w, r)
	case r.Method == http.MethodPatch && pathMatchesID(path, "admin", "library-cdks"):
		s.adminLibraryCDKStatus(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/share-codes":
		s.adminShareCodes(w, r)
	case r.Method == http.MethodPost && path == "/admin/share-codes":
		s.adminCreateShareCode(w, r)
	case r.Method == http.MethodGet && pathMatchesID(path, "admin", "share-codes"):
		s.adminShareCodeDetail(w, r, segment(path, 2))
	case r.Method == http.MethodDelete && pathMatchesID(path, "admin", "share-codes"):
		s.adminDeleteShareCode(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/textbooks":
		s.adminTextbooks(w, r)
	case r.Method == http.MethodPost && path == "/admin/textbooks":
		s.adminTextbookCreate(w, r)
	case r.Method == http.MethodPut && pathMatchesID(path, "admin", "textbooks"):
		s.adminTextbookUpdate(w, r, segment(path, 2))
	case r.Method == http.MethodDelete && pathMatchesID(path, "admin", "textbooks"):
		s.adminTextbookDelete(w, r, segment(path, 2))
	case r.Method == http.MethodPost && pathMatchesIDSuffix(path, "pdf", "admin", "textbooks"):
		s.adminTextbookUploadPdf(w, r, segment(path, 2))
	case r.Method == http.MethodPost && pathMatchesIDSuffix(path, "cover", "admin", "textbooks"):
		s.adminTextbookUploadCover(w, r, segment(path, 2))
	case r.Method == http.MethodGet && pathMatchesIDSuffix(path, "resource-url", "admin", "textbooks"):
		s.adminTextbookResourceURL(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/resources":
		s.adminResources(w, r)
	case r.Method == http.MethodPost && path == "/admin/resources/upload":
		s.adminResourceUpload(w, r)
	case r.Method == http.MethodDelete && pathMatchesID(path, "admin", "resources"):
		s.adminResourceDelete(w, r, segment(path, 2))
	case r.Method == http.MethodGet && pathMatchesIDSuffix(path, "resource-url", "admin", "resources"):
		s.adminResourceResourceURL(w, r, segment(path, 2))
	case r.Method == http.MethodGet && path == "/admin/courses":
		s.adminCourses(w, r)
	case r.Method == http.MethodPost && path == "/admin/courses":
		s.adminCourseCreate(w, r)
	case r.Method == http.MethodPut && pathMatchesID(path, "admin", "courses"):
		s.adminCourseUpdate(w, r, segment(path, 2))
	case r.Method == http.MethodDelete && pathMatchesID(path, "admin", "courses"):
		s.adminCourseDelete(w, r, segment(path, 2))
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
	if entry, ok := s.rates[key]; ok {
		if entry.started.IsZero() || now.Sub(entry.started) >= window {
			s.rates[key] = rateWindow{started: now, count: 1}
			return true
		}
		if entry.count >= limit {
			return false
		}
		entry.count++
		s.rates[key] = entry
		return true
	}
	if len(s.rates) >= maxRateEntries {
		// Keep the per-source limiter bounded even when every source is still
		// in its active window. Remove expired entries first, then evict the
		// oldest window before inserting the new source.
		var oldestKey string
		var oldest time.Time
		for k, v := range s.rates {
			if now.Sub(v.started) >= window {
				delete(s.rates, k)
				continue
			}
			if oldest.IsZero() || v.started.Before(oldest) {
				oldestKey, oldest = k, v.started
			}
		}
		if len(s.rates) >= maxRateEntries && oldestKey != "" {
			delete(s.rates, oldestKey)
		}
	}
	s.rates[key] = rateWindow{started: now, count: 1}
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

type adminPrincipalKey struct{}

func (s *Server) authAdmin(w http.ResponseWriter, r *http.Request) (app.AdminPrincipal, bool) {
	if p, ok := r.Context().Value(adminPrincipalKey{}).(app.AdminPrincipal); ok {
		return p, true
	}
	p, err := s.App.AuthenticateAdmin(r.Context(), adminToken(r))
	if err != nil {
		writeError(w, r, err)
		return p, false
	}
	return p, true
}

// adminPageFor maps an administrator API path to its console page and whether
// the request mutates data. An empty page means any authenticated admin may
// call it (for example /admin/me).
func adminPageFor(path, method string) (string, bool) {
	write := method != http.MethodGet && method != http.MethodHead
	switch {
	case path == "/admin/metrics/overview" || path == "/admin/metrics/series" ||
		path == "/admin/metrics/breakdown" || path == "/admin/overview-display" ||
		path == "/admin/cdn/stats" || path == "/admin/host/stats" || path == "/admin/buckets/stats":
		return "overview", write
	case path == "/admin/users" || strings.HasPrefix(path, "/admin/users/"):
		return "users", write
	case path == "/admin/devices" || strings.HasPrefix(path, "/admin/devices/"):
		return "devices", write
	case path == "/admin/risk-events" || strings.HasPrefix(path, "/admin/risk-events/"):
		return "risk", write
	case path == "/admin/error-reports" || strings.HasPrefix(path, "/admin/error-reports/"):
		return "error-reports", write
	case path == "/admin/question-banks" || strings.HasPrefix(path, "/admin/question-banks/") ||
		path == "/admin/library-cdks" || strings.HasPrefix(path, "/admin/library-cdks/"):
		return "library", write
	case path == "/admin/textbooks" || strings.HasPrefix(path, "/admin/textbooks/"):
		return "textbooks", write
	case path == "/admin/resources" || strings.HasPrefix(path, "/admin/resources/"):
		return "resources", write
	case path == "/admin/courses" || strings.HasPrefix(path, "/admin/courses/"):
		return "courses", write
	case path == "/admin/share-codes" || strings.HasPrefix(path, "/admin/share-codes/"):
		return "share-codes", write
	case path == "/admin/app/release" || path == "/admin/app/release-test" || path == "/admin/school-calendar":
		return "config", write
	case path == "/admin/audit":
		return "audit", write
	case path == "/admin/admins" || strings.HasPrefix(path, "/admin/admins/"):
		return "admins", write
	}
	return "", false
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
func (s *Server) loginEligibility(w http.ResponseWriter, r *http.Request) {
	// AuthenticateDevice rejects a revoked device before the body is read.
	if _, ok := s.authDevice(w, r); !ok {
		return
	}
	var in struct {
		StudentID string `json:"student_id"`
	}
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if err := s.App.CheckLoginEligibility(r.Context(), in.StudentID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"allowed": true})
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
	token, principal, err := s.App.AdminLogin(r.Context(), in.Key)
	if err != nil {
		writeError(w, r, err)
		return
	}
	csrf := newRequestID()
	secure := strings.HasPrefix(strings.ToLower(s.App.Cfg.PublicBaseURL), "https://")
	http.SetCookie(w, &http.Cookie{Name: "control_admin", Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	http.SetCookie(w, &http.Cookie{Name: "control_csrf", Value: csrf, Path: "/", HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token, "expires_in": 43200, "csrf_token": csrf,
		"name": principal.Admin.Name, "permissions": principal.Admin.Permissions,
	})
}

func (s *Server) adminMe(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.App.AdminMe(p))
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
func (s *Server) adminOverviewDisplay(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scalePercent": s.App.OverviewScalePercent()})
}

func (s *Server) adminOverviewDisplayUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		ScalePercent int `json:"scalePercent"`
	}
	if err := decodeJSON(r, &in, 4<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if err := s.App.SetOverviewScalePercent(r.Context(), in.ScalePercent, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scalePercent": in.ScalePercent})
}
func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListUsers(r.Context(), limit, offset, r.URL.Query().Get("status"), r.URL.Query().Get("q"), r.URL.Query().Get("empty"), r.URL.Query().Get("sort"), r.URL.Query().Get("order"))
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
func (s *Server) adminDeviceStatus(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if err := s.App.SetDeviceStatus(r.Context(), id, in.Status, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "status": in.Status})
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
func (s *Server) adminRiskClear(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	count, err := s.App.ClearRisk(r.Context(), p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": count})
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
