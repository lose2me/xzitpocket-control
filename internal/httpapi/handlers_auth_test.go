package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// adminRoutes lists every administrator route with a method. Requests without
// a token must be rejected before the handler runs.
var adminRoutes = []struct {
	method string
	path   string
	body   any
}{
	{http.MethodGet, "/api/v1/admin/me", nil},
	{http.MethodDelete, "/api/v1/admin/session", nil},
	{http.MethodGet, "/api/v1/admin/admins", nil},
	{http.MethodPost, "/api/v1/admin/admins", map[string]any{}},
	{http.MethodPut, "/api/v1/admin/admins/x", map[string]any{}},
	{http.MethodDelete, "/api/v1/admin/admins/x", nil},
	{http.MethodGet, "/api/v1/admin/metrics/overview", nil},
	{http.MethodGet, "/api/v1/admin/metrics/series", nil},
	{http.MethodGet, "/api/v1/admin/metrics/breakdown", nil},
	{http.MethodGet, "/api/v1/admin/cdn/stats", nil},
	{http.MethodGet, "/api/v1/admin/host/stats", nil},
	{http.MethodGet, "/api/v1/admin/buckets/stats", nil},
	{http.MethodGet, "/api/v1/admin/overview-display", nil},
	{http.MethodPut, "/api/v1/admin/overview-display", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/users", nil},
	{http.MethodGet, "/api/v1/admin/users/x", nil},
	{http.MethodPatch, "/api/v1/admin/users/x/status", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/devices", nil},
	{http.MethodPatch, "/api/v1/admin/devices/x/status", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/risk-events", nil},
	{http.MethodDelete, "/api/v1/admin/risk-events", nil},
	{http.MethodPatch, "/api/v1/admin/risk-events/x", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/audit", nil},
	{http.MethodGet, "/api/v1/admin/error-reports", nil},
	{http.MethodDelete, "/api/v1/admin/error-reports", nil},
	{http.MethodPatch, "/api/v1/admin/error-reports/1", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/app/release", nil},
	{http.MethodPut, "/api/v1/admin/app/release", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/app/release-test", nil},
	{http.MethodPut, "/api/v1/admin/app/release-test", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/settings", nil},
	{http.MethodPut, "/api/v1/admin/settings", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/school-calendar", nil},
	{http.MethodPut, "/api/v1/admin/school-calendar", map[string]any{}},
	{http.MethodPut, "/api/v1/admin/school-calendar/exceptions", map[string]any{}},
	{http.MethodDelete, "/api/v1/admin/school-calendar/exceptions", nil},
	{http.MethodGet, "/api/v1/admin/question-banks", nil},
	{http.MethodPost, "/api/v1/admin/question-banks", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/question-banks/x", nil},
	{http.MethodPut, "/api/v1/admin/question-banks/x", map[string]any{}},
	{http.MethodDelete, "/api/v1/admin/question-banks/x", nil},
	{http.MethodPatch, "/api/v1/admin/question-banks/x/status", map[string]any{}},
	{http.MethodPost, "/api/v1/admin/question-banks/x/archive", nil},
	{http.MethodPost, "/api/v1/admin/question-banks/x/restore", nil},
	{http.MethodGet, "/api/v1/admin/library-cdks", nil},
	{http.MethodPost, "/api/v1/admin/library-cdks", map[string]any{}},
	{http.MethodPatch, "/api/v1/admin/library-cdks/x", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/share-codes", nil},
	{http.MethodPost, "/api/v1/admin/share-codes", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/share-codes/x", nil},
	{http.MethodDelete, "/api/v1/admin/share-codes/x", nil},
	{http.MethodGet, "/api/v1/admin/textbooks", nil},
	{http.MethodPost, "/api/v1/admin/textbooks", map[string]any{}},
	{http.MethodPut, "/api/v1/admin/textbooks/x", map[string]any{}},
	{http.MethodDelete, "/api/v1/admin/textbooks/x", nil},
	{http.MethodPost, "/api/v1/admin/textbooks/x/archive", nil},
	{http.MethodPost, "/api/v1/admin/textbooks/x/restore", nil},
	{http.MethodGet, "/api/v1/admin/textbooks/x/resource-url", nil},
	{http.MethodGet, "/api/v1/admin/textbooks/x/download", nil},
	{http.MethodGet, "/api/v1/admin/textbook-submissions", nil},
	{http.MethodPost, "/api/v1/admin/textbook-submissions/x/reject", nil},
	{http.MethodDelete, "/api/v1/admin/textbook-submissions/x/file", nil},
	{http.MethodGet, "/api/v1/admin/resource-submissions", nil},
	{http.MethodPost, "/api/v1/admin/resource-submissions/x/reject", nil},
	{http.MethodGet, "/api/v1/admin/resources", nil},
	{http.MethodDelete, "/api/v1/admin/resources/x", nil},
	{http.MethodGet, "/api/v1/admin/resources/x/download", nil},
	{http.MethodPost, "/api/v1/admin/resources/x/archive", nil},
	{http.MethodPost, "/api/v1/admin/resources/x/restore", nil},
	{http.MethodGet, "/api/v1/admin/ai-analyses", nil},
	{http.MethodPost, "/api/v1/admin/ai-analyses", map[string]any{}},
	{http.MethodGet, "/api/v1/admin/ai-analyses/x", nil},
	{http.MethodPost, "/api/v1/admin/ai-analyses/x/retry", nil},
	{http.MethodPost, "/api/v1/admin/ai-analyses/x/cancel", nil},
	{http.MethodGet, "/api/v1/admin/ai-analyses/x/download", nil},
	{http.MethodGet, "/api/v1/admin/courses", nil},
	{http.MethodPost, "/api/v1/admin/courses", map[string]any{}},
	{http.MethodPut, "/api/v1/admin/courses/x", map[string]any{}},
	{http.MethodDelete, "/api/v1/admin/courses/x", nil},
}

func TestAdminRoutesRequireAuthentication(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	for _, route := range adminRoutes {
		status := requestStatus(t, ts.URL+route.path, route.method, route.body, nil)
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d, want 401", route.method, route.path, status)
		}
		// A user session token is not an administrator token.
		status = requestStatus(t, ts.URL+route.path, route.method, route.body, map[string]string{"Authorization": "Bearer access"})
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s with user token = %d, want 401", route.method, route.path, status)
		}
	}
}

// userRoutes are authenticated with a user session; without one they must fail.
var userRoutes = []struct {
	method string
	path   string
	body   any
}{
	{http.MethodGet, "/api/v1/me", nil},
	{http.MethodGet, "/api/v1/me/devices", nil},
	{http.MethodPost, "/api/v1/auth/refresh", map[string]any{}},
	{http.MethodPost, "/api/v1/auth/revoke", nil},
	{http.MethodGet, "/api/v1/resource-center/courses", nil},
	{http.MethodGet, "/api/v1/resource-center/upload-stats", nil},
	{http.MethodGet, "/api/v1/resource-center/upload-textbooks", nil},
	{http.MethodGet, "/api/v1/resources", nil},
	{http.MethodGet, "/api/v1/textbooks", nil},
	{http.MethodGet, "/api/v1/resources/x/ai", nil},
	{http.MethodGet, "/api/v1/resources/x/url", nil},
	{http.MethodGet, "/api/v1/textbooks/x/url", nil},
	{http.MethodGet, "/api/v1/question-banks", nil},
	{http.MethodGet, "/api/v1/question-banks/x", nil},
	{http.MethodPost, "/api/v1/telemetry/events", []any{}},
	{http.MethodPost, "/api/v1/error-reports", map[string]any{}},
	{http.MethodPost, "/api/v1/courses", map[string]any{}},
	{http.MethodPost, "/api/v1/share-codes", map[string]any{"suffix": "1", "data": map[string]any{"a": 1}}},
	{http.MethodPost, "/api/v1/library/cdks/redeem", map[string]any{"code": "AAAAA1"}},
	{http.MethodPost, "/api/v1/resource-center/submissions", map[string]any{}},
	{http.MethodPost, "/api/v1/resource-center/submissions/x/complete", nil},
	{http.MethodDelete, "/api/v1/resource-center/submissions/x", nil},
}

func TestUserRoutesRequireSession(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	for _, route := range userRoutes {
		status := requestStatus(t, ts.URL+route.path, route.method, route.body, map[string]string{"X-Student-ID": testStudent})
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s without session = %d, want 401", route.method, route.path, status)
		}
	}
}

func TestAdminMeAndLogout(t *testing.T) {
	ts, _, adminToken := newAdminServer(t)
	admin := map[string]string{"Authorization": adminToken}
	me := requestWithHeaders(t, ts.URL+"/api/v1/admin/me", http.MethodGet, nil, admin)
	if me["permissions"] != "*=rw" {
		t.Fatalf("admin me = %#v", me)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/session", http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("admin logout = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/me", http.MethodGet, nil, admin); status != http.StatusUnauthorized {
		t.Fatalf("admin me after logout = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": ""}, nil); status != http.StatusUnauthorized {
		t.Fatalf("empty admin key = %d", status)
	}
}

func TestAdminCookieSessionsRequireCSRF(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	login := request(t, ts.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "test-password"}, nil)
	adminCookie := login["access_token"].(string)
	csrf := login["csrf_token"].(string)
	cookieHeader := "control_admin=" + adminCookie + "; control_csrf=" + csrf

	// Reading with cookies works without the CSRF header.
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", cookieHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie me = %d", resp.StatusCode)
	}

	cookieRequest := func(method, path, cookie, token string) int {
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", cookie)
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := cookieRequest(http.MethodPut, "/api/v1/admin/app/release", cookieHeader, ""); status != http.StatusForbidden {
		t.Fatalf("cookie write without csrf = %d", status)
	}
	if status := cookieRequest(http.MethodPut, "/api/v1/admin/app/release", cookieHeader, "wrong"); status != http.StatusForbidden {
		t.Fatalf("cookie write with wrong csrf = %d", status)
	}
	if status := cookieRequest(http.MethodPut, "/api/v1/admin/app/release", "control_admin="+adminCookie, csrf); status != http.StatusForbidden {
		t.Fatalf("missing csrf cookie = %d", status)
	}
	if status := cookieRequest(http.MethodPut, "/api/v1/admin/app/release", cookieHeader, csrf); status != http.StatusBadRequest {
		t.Fatalf("valid csrf should reach the handler = %d", status)
	}
	if status := cookieRequest(http.MethodDelete, "/api/v1/admin/session", cookieHeader, csrf); status != http.StatusOK {
		t.Fatalf("cookie logout = %d", status)
	}
}

func TestTelemetryAuthenticationAndValidation(t *testing.T) {
	ts, headers, _ := newAdminServer(t)
	if status := requestStatus(t, ts.URL+"/api/v1/telemetry/events", http.MethodPost, "not-json", headers); status != http.StatusBadRequest {
		t.Fatalf("malformed telemetry = %d", status)
	}
	// Unknown device tokens are refused.
	if status := requestStatus(t, ts.URL+"/api/v1/telemetry/events", http.MethodPost, []any{}, map[string]string{"Authorization": "Device unknown", "X-Student-ID": testStudent}); status != http.StatusUnauthorized {
		t.Fatalf("unknown device telemetry = %d", status)
	}
	// Events are accepted for the signed-in session.
	events := []any{map[string]any{"event_id": "evt-a", "type": "app_start", "occurred_at": time.Now().UTC().Format(time.RFC3339), "properties": map[string]any{"platform": "android", "app_version": "9.9.9"}}}
	if status := requestStatus(t, ts.URL+"/api/v1/telemetry/events", http.MethodPost, events, headers); status != http.StatusAccepted {
		t.Fatalf("telemetry = %d", status)
	}
	// A device principal may report without a user session.
	device := newTestDevice(t, "telemetry-device")
	registerDevice(t, ts.URL, device)
	deviceHeaders := map[string]string{"Authorization": "Device " + device.token, "X-Student-ID": testStudent}
	if status := requestStatus(t, ts.URL+"/api/v1/telemetry/events", http.MethodPost, events, deviceHeaders); status != http.StatusAccepted {
		t.Fatalf("device telemetry = %d", status)
	}
	// An unknown share code is a plain 404 for anyone.
	if status := requestStatus(t, ts.URL+"/api/v1/share-codes/AAAAA1", http.MethodGet, nil, nil); status != http.StatusNotFound {
		t.Fatalf("unknown share code = %d", status)
	}
}
