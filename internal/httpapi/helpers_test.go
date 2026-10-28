package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/app"
)

func TestDecodeJSONRejectsInvalidBodies(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	cases := map[string]struct {
		body string
		max  int64
		ok   bool
	}{
		"valid":          {`{"name":"x"}`, 64, true},
		"unknown field":  {`{"name":"x","extra":1}`, 64, false},
		"trailing value": {`{"name":"x"}{"name":"y"}`, 64, false},
		"empty":          {``, 64, false},
		"too large":      {`{"name":"` + strings.Repeat("x", 100) + `"}`, 32, false},
		"trailing junk":  {`{"name":"x"}junk`, 64, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			err := decodeJSON(req, &payload{}, tc.max)
			if tc.ok && err != nil {
				t.Fatalf("decodeJSON = %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestPathHelpers(t *testing.T) {
	for _, tc := range []struct {
		path   string
		prefix []string
		want   bool
	}{
		{"/admin/users", []string{"admin", "users"}, false},
		{"/admin/users/abc", []string{"admin", "users"}, true},
		{"/admin/users/", []string{"admin", "users"}, false},
		{"/admin/users/abc/extra", []string{"admin", "users"}, false},
		{"/other/users/abc", []string{"admin", "users"}, false},
	} {
		if got := pathMatchesID(tc.path, tc.prefix...); got != tc.want {
			t.Fatalf("pathMatchesID(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	for _, tc := range []struct {
		path, suffix string
		prefix       []string
		want         bool
	}{
		{"/admin/users/abc/status", "status", []string{"admin", "users"}, true},
		{"/admin/users/abc/other", "status", []string{"admin", "users"}, false},
		{"/admin/users//status", "status", []string{"admin", "users"}, false},
		{"/admin/users/status", "status", []string{"admin", "users"}, false},
	} {
		if got := pathMatchesIDSuffix(tc.path, tc.suffix, tc.prefix...); got != tc.want {
			t.Fatalf("pathMatchesIDSuffix(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	if got := segment("/admin/users/abc/status", 2); got != "abc" {
		t.Fatalf("segment = %q", got)
	}
	if got := segment("/admin/users", 5); got != "" {
		t.Fatalf("segment out of range = %q", got)
	}
	if got := segment("/admin/users", -1); got != "" {
		t.Fatalf("segment negative = %q", got)
	}
}

func TestParseIntQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?limit=7&offset=nope&big=999999999999999999999", nil)
	if got := parseIntQuery(req, "limit", 50); got != 7 {
		t.Fatalf("limit = %d", got)
	}
	if got := parseIntQuery(req, "offset", 50); got != 50 {
		t.Fatalf("invalid offset = %d", got)
	}
	if got := parseIntQuery(req, "missing", 5); got != 5 {
		t.Fatalf("missing = %d", got)
	}
	if got := parseIntQuery(req, "big", 3); got != 3 {
		t.Fatalf("overflow = %d", got)
	}
}

func TestTokenParsing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if bearer(req) != "" || deviceToken(req) != "" || adminToken(req) != "" || sessionToken(req) != "" {
		t.Fatal("empty request should have no tokens")
	}
	req.Header.Set("Authorization", "bearer  access-token  ")
	if bearer(req) != "access-token" {
		t.Fatalf("bearer = %q", bearer(req))
	}
	if adminToken(req) != "access-token" || sessionToken(req) != "access-token" {
		t.Fatal("bearer should win for admin and session tokens")
	}
	req.Header.Set("Authorization", "Device dev-token")
	if deviceToken(req) != "dev-token" || bearer(req) != "" {
		t.Fatalf("device token = %q bearer = %q", deviceToken(req), bearer(req))
	}
	req.Header.Set("Authorization", "Basic x")
	if bearer(req) != "" || deviceToken(req) != "" {
		t.Fatal("unknown schemes should be ignored")
	}

	cookieReq := httptest.NewRequest(http.MethodGet, "/", nil)
	cookieReq.AddCookie(&http.Cookie{Name: "control_admin", Value: "admin-cookie"})
	cookieReq.AddCookie(&http.Cookie{Name: "control_access", Value: "access-cookie"})
	if adminToken(cookieReq) != "admin-cookie" || sessionToken(cookieReq) != "access-cookie" {
		t.Fatal("cookie tokens should be used when no bearer is present")
	}
}

func TestRequestIDHelpers(t *testing.T) {
	if id := newRequestID(); !strings.HasPrefix(id, "req_") || len(id) < 10 {
		t.Fatalf("newRequestID = %q", id)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if requestID(req) != "req_unknown" {
		t.Fatalf("missing request id = %q", requestID(req))
	}
	withID := req.WithContext(context.WithValue(req.Context(), requestIDKey{}, "req_test"))
	if requestID(withID) != "req_test" {
		t.Fatalf("request id = %q", requestID(withID))
	}
}

func TestAdminPageMapping(t *testing.T) {
	cases := []struct {
		path, method string
		page         string
		write        bool
	}{
		{"/admin/metrics/overview", http.MethodGet, "overview", false},
		{"/admin/cdn/stats", http.MethodGet, "overview", false},
		{"/admin/users", http.MethodGet, "users", false},
		{"/admin/users/x/status", http.MethodPatch, "users", true},
		{"/admin/devices", http.MethodGet, "devices", false},
		{"/admin/risk-events", http.MethodDelete, "risk", true},
		{"/admin/error-reports", http.MethodGet, "error-reports", false},
		{"/admin/question-banks", http.MethodGet, "library", false},
		{"/admin/library-cdks/x", http.MethodPatch, "library", true},
		{"/admin/textbooks", http.MethodGet, "textbooks", false},
		{"/admin/textbook-submissions/x/download", http.MethodGet, "textbook-submissions", false},
		{"/admin/resources", http.MethodGet, "resources", false},
		{"/admin/ai-analyses/x/retry", http.MethodPost, "resources", true},
		{"/admin/resource-submissions", http.MethodGet, "resource-submissions", false},
		{"/admin/courses", http.MethodGet, "courses", false},
		{"/admin/share-codes", http.MethodGet, "share-codes", false},
		{"/admin/app/release", http.MethodPut, "app-release", true},
		{"/admin/school-calendar", http.MethodGet, "school-calendar", false},
		{"/admin/settings", http.MethodPut, "", false},
		{"/admin/me", http.MethodGet, "", false},
		{"/admin/session", http.MethodPost, "", false},
		{"/admin/audit", http.MethodGet, "audit", false},
		{"/admin/admins", http.MethodGet, "admins", false},
		{"/admin/unknown", http.MethodGet, "unassigned", false},
	}
	for _, tc := range cases {
		page, write := adminPageFor(tc.path, tc.method)
		if page != tc.page || write != tc.write {
			t.Fatalf("adminPageFor(%q, %q) = %q,%v want %q,%v", tc.path, tc.method, page, write, tc.page, tc.write)
		}
	}
}

func TestAllowRateLimiter(t *testing.T) {
	server := &Server{rates: make(map[string]rateWindow)}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	if !server.allow(req, "bucket", 2, time.Minute) {
		t.Fatal("first call should pass")
	}
	if !server.allow(req, "bucket", 2, time.Minute) {
		t.Fatal("second call should pass")
	}
	if server.allow(req, "bucket", 2, time.Minute) {
		t.Fatal("third call should be limited")
	}
	if !server.allow(req, "other", 2, time.Minute) {
		t.Fatal("other buckets are independent")
	}
	// An expired window resets the counter.
	server.rates["bucket|10.0.0.1"] = rateWindow{started: time.Now().Add(-2 * time.Minute), count: 99}
	if !server.allow(req, "bucket", 2, time.Minute) {
		t.Fatal("expired window should reset")
	}
	// A missing RemoteAddr port is used as-is.
	bare := httptest.NewRequest(http.MethodPost, "/", nil)
	bare.RemoteAddr = "10.0.0.2"
	if !server.allow(bare, "bucket", 5, time.Minute) {
		t.Fatal("bare remote addr should work")
	}
}

func TestAllowRateLimiterEvictsWhenFull(t *testing.T) {
	now := time.Now()
	rates := make(map[string]rateWindow, maxRateEntries)
	for i := 0; i < maxRateEntries/2; i++ {
		// Half of the entries are expired and get pruned.
		rates[fmt.Sprintf("old|10.0.%d.%d", i/250, i%250)] = rateWindow{started: now.Add(-time.Hour), count: 1}
	}
	for i := 0; len(rates) < maxRateEntries; i++ {
		rates[fmt.Sprintf("fresh|10.1.%d.%d", i/250, i%250)] = rateWindow{started: now, count: 1}
	}
	server := &Server{rates: rates}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.9.9.9"
	if !server.allow(req, "new", 5, time.Minute) {
		t.Fatal("new source should still be allowed")
	}
	if len(server.rates) > maxRateEntries {
		t.Fatalf("rates = %d, want bounded", len(server.rates))
	}

	// When every entry is fresh the oldest is evicted.
	full := make(map[string]rateWindow, maxRateEntries)
	for i := 0; i < maxRateEntries; i++ {
		full[fmt.Sprintf("fresh|10.2.%d.%d", i/250, i%250)] = rateWindow{started: now.Add(-time.Minute), count: 1}
	}
	server = &Server{rates: full}
	if !server.allow(req, "new", 5, time.Minute) {
		t.Fatal("oldest entry should be evicted")
	}
	if len(server.rates) > maxRateEntries {
		t.Fatalf("rates = %d, want bounded after eviction", len(server.rates))
	}
	// A server built without a map still works.
	empty := &Server{}
	if !empty.allow(req, "bucket", 1, time.Minute) {
		t.Fatal("nil map should be initialised")
	}
}

func TestWriteErrorMapsAPIErrors(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	writeError(recorder, req, app.Err("invalid", "参数无效", http.StatusBadRequest))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "参数无效") {
		t.Fatalf("api error = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	writeError(recorder, req, app.ErrUnauthorized)
	if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized = %d %v", recorder.Code, recorder.Header())
	}
	recorder = httptest.NewRecorder()
	writeError(recorder, req, errString("boom"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unknown error = %d", recorder.Code)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestDownloadHelpers(t *testing.T) {
	cases := map[string]string{
		"a.pdf":  "application/pdf",
		"a.docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"a.doc":  "application/msword",
		"a.bin":  "application/pdf",
	}
	for name, want := range cases {
		if got := downloadContentType(name); got != want {
			t.Fatalf("downloadContentType(%q) = %q", name, got)
		}
	}
	disposition := downloadContentDisposition("高数 试卷.pdf")
	if !strings.HasPrefix(disposition, "attachment; filename=") || !strings.Contains(disposition, "filename*=UTF-8''") {
		t.Fatalf("disposition = %q", disposition)
	}
	if fallback := downloadContentDisposition("中文.docx"); !strings.Contains(fallback, `filename="__.docx"`) {
		t.Fatalf("non-ascii fallback = %q", fallback)
	}
	if empty := downloadContentDisposition(""); !strings.Contains(empty, `filename="download"`) {
		t.Fatalf("empty fallback = %q", empty)
	}
	body := "pdf-bytes"
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(body)), ContentLength: int64(len(body))}
	recorder := httptest.NewRecorder()
	streamDownload(recorder, resp, "试卷.docx")
	if recorder.Code != http.StatusOK || recorder.Body.String() != body {
		t.Fatalf("stream = %d %q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("stream headers = %v", recorder.Header())
	}
	if recorder.Header().Get("Content-Length") != "9" {
		t.Fatalf("content length = %q", recorder.Header().Get("Content-Length"))
	}
}
