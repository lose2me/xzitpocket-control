package httpapi

import (
	"net/http"
	"testing"
)

// TestAdminEndpointsSurfaceStorageErrors drops the table behind each read
// endpoint after the admin logged in. Authentication keeps working, so the
// handler's error branch (writer/500) is exercised instead of the auth guard.
func TestAdminEndpointsSurfaceStorageErrors(t *testing.T) {
	cases := []struct {
		table, method, path string
	}{
		{"activity_events", http.MethodGet, "/api/v1/admin/metrics/overview"},
		{"activity_events", http.MethodGet, "/api/v1/admin/metrics/series?days=7"},
		{"activity_events", http.MethodGet, "/api/v1/admin/metrics/breakdown"},
		{"audit_logs", http.MethodGet, "/api/v1/admin/audit"},
		{"risk_events", http.MethodGet, "/api/v1/admin/risk-events"},
		{"risk_events", http.MethodDelete, "/api/v1/admin/risk-events"},
		{"error_reports", http.MethodGet, "/api/v1/admin/error-reports"},
		{"error_reports", http.MethodDelete, "/api/v1/admin/error-reports"},
		{"devices", http.MethodGet, "/api/v1/admin/devices"},
		{"users", http.MethodGet, "/api/v1/admin/users"},
		{"app_release_config", http.MethodGet, "/api/v1/admin/app/release"},
		{"app_release_test_config", http.MethodGet, "/api/v1/admin/app/release-test"},
		{"school_calendar_config", http.MethodGet, "/api/v1/admin/school-calendar"},
		{"question_banks", http.MethodGet, "/api/v1/admin/question-banks"},
		{"library_cdks", http.MethodGet, "/api/v1/admin/library-cdks"},
		{"share_codes", http.MethodGet, "/api/v1/admin/share-codes"},
		{"textbooks", http.MethodGet, "/api/v1/admin/textbooks"},
		{"resources", http.MethodGet, "/api/v1/admin/resources"},
		{"resource_ai_tasks", http.MethodGet, "/api/v1/admin/ai-analyses"},
		{"courses", http.MethodGet, "/api/v1/admin/courses"},
		{"resource_submissions", http.MethodGet, "/api/v1/admin/textbook-submissions"},
		{"resource_submissions", http.MethodGet, "/api/v1/admin/resource-submissions"},
		{"admin_sessions", http.MethodGet, "/api/v1/admin/me"},
	}
	for _, tc := range cases {
		t.Run(tc.table+tc.path, func(t *testing.T) {
			f := newAdminFixture(t, http.NotFoundHandler())
			admin := map[string]string{"Authorization": f.token}
			if _, err := f.store.DB.Exec("PRAGMA foreign_keys = OFF"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.DB.Exec("DROP TABLE " + tc.table); err != nil {
				t.Fatal(err)
			}
			status := requestStatus(t, f.server.URL+tc.path, tc.method, nil, admin)
			if status != http.StatusInternalServerError {
				t.Fatalf("%s with dropped %s = %d, want 500", tc.path, tc.table, status)
			}
		})
	}
}

func TestUserEndpointsSurfaceStorageErrors(t *testing.T) {
	cases := []struct {
		table, method, path string
	}{
		{"sessions", http.MethodGet, "/api/v1/me"},
		{"sessions", http.MethodGet, "/api/v1/me/devices"},
		{"sessions", http.MethodPost, "/api/v1/auth/revoke"},
	}
	for _, tc := range cases {
		t.Run(tc.table+tc.path, func(t *testing.T) {
			f := newAdminFixture(t, http.NotFoundHandler())
			user := f.headers
			if _, err := f.store.DB.Exec("PRAGMA foreign_keys = OFF"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.DB.Exec("DROP TABLE " + tc.table); err != nil {
				t.Fatal(err)
			}
			status := requestStatus(t, f.server.URL+tc.path, tc.method, nil, user)
			_ = status
			// Without the session table the token cannot be resolved: the
			// storage error surfaces instead of a generic 401.
			if status != http.StatusInternalServerError {
				t.Fatalf("%s with dropped %s = %d, want 500", tc.path, tc.table, status)
			}
		})
	}
}

// TestWriteEndpointsSurfaceStorageErrors drops the table behind each mutation
// after authentication, so the handler's failure branch is exercised.
func TestWriteEndpointsSurfaceStorageErrors(t *testing.T) {
	validBank := map[string]any{"questionBank": map[string]any{"name": "题库", "questions": []any{}}}
	adminCases := []struct {
		table, method, path string
		body                any
	}{
		{"app_release_config", http.MethodPut, "/api/v1/admin/app/release", map[string]any{"latestVersion": "2.0.0", "downloadUrl": "https://dl.xuda.live/a.apk"}},
		{"app_release_test_config", http.MethodPut, "/api/v1/admin/app/release-test", map[string]any{"latestVersion": "2.0.0", "downloadUrl": "https://dl.xuda.live/a.apk"}},
		{"school_calendar_config", http.MethodPut, "/api/v1/admin/school-calendar", map[string]any{"days": []any{map[string]any{"date": "2026-09-01"}}}},
		{"school_calendar_colleges", http.MethodPut, "/api/v1/admin/school-calendar/exceptions", map[string]any{"college": "计算机学院", "exceptions": []any{}}},
		{"school_calendar_colleges", http.MethodDelete, "/api/v1/admin/school-calendar/exceptions?college=x", nil},
		{"textbooks", http.MethodPost, "/api/v1/admin/textbooks", map[string]any{"name": "教材", "tags": "", "course_codes": []string{"C1"}}},
		{"textbooks", http.MethodPut, "/api/v1/admin/textbooks/t1", map[string]any{"name": "教材", "tags": "", "course_codes": []string{"C1"}}},
		{"textbooks", http.MethodDelete, "/api/v1/admin/textbooks/t1", nil},
		{"textbooks", http.MethodPost, "/api/v1/admin/textbooks/t1/archive", nil},
		{"textbooks", http.MethodPost, "/api/v1/admin/textbooks/t1/restore", nil},
		{"resources", http.MethodDelete, "/api/v1/admin/resources/r1", nil},
		{"resources", http.MethodPost, "/api/v1/admin/resources/r1/archive", nil},
		{"resources", http.MethodPost, "/api/v1/admin/resources/r1/restore", nil},
		{"courses", http.MethodPost, "/api/v1/admin/courses", map[string]any{"code": "C9", "name": "课程"}},
		{"courses", http.MethodPut, "/api/v1/admin/courses/c1", map[string]any{"code": "C1", "name": "课程"}},
		{"courses", http.MethodDelete, "/api/v1/admin/courses/c1", nil},
		{"question_banks", http.MethodPost, "/api/v1/admin/question-banks", validBank},
		{"question_banks", http.MethodPut, "/api/v1/admin/question-banks/b1", validBank},
		{"question_banks", http.MethodPatch, "/api/v1/admin/question-banks/b1/status", map[string]any{"status": "active"}},
		{"question_banks", http.MethodPost, "/api/v1/admin/question-banks/b1/archive", nil},
		{"question_banks", http.MethodPost, "/api/v1/admin/question-banks/b1/restore", nil},
		{"question_banks", http.MethodDelete, "/api/v1/admin/question-banks/b1", nil},
		{"library_cdks", http.MethodPost, "/api/v1/admin/library-cdks", map[string]any{"count": 1}},
		{"library_cdks", http.MethodPatch, "/api/v1/admin/library-cdks/c1", map[string]any{"status": "disabled"}},
		{"share_codes", http.MethodPost, "/api/v1/admin/share-codes", map[string]any{"suffix": "2", "data": map[string]any{"a": 1}}},
		{"share_codes", http.MethodDelete, "/api/v1/admin/share-codes/s1", nil},
		{"users", http.MethodPatch, "/api/v1/admin/users/user/status", map[string]any{"status": "active"}},
		{"devices", http.MethodPatch, "/api/v1/admin/devices/device/status", map[string]any{"status": "revoked"}},
		{"risk_events", http.MethodPatch, "/api/v1/admin/risk-events/r1", map[string]any{}},
		{"error_reports", http.MethodPatch, "/api/v1/admin/error-reports/1", map[string]any{"ignored": true}},
		{"admin_sessions", http.MethodPost, "/api/v1/admin/session", map[string]any{"key": "test-password"}},
	}
	for _, tc := range adminCases {
		t.Run(tc.table+tc.method+tc.path, func(t *testing.T) {
			f := newAdminFixture(t, http.NotFoundHandler())
			admin := map[string]string{"Authorization": f.token}
			if _, err := f.store.DB.Exec("PRAGMA foreign_keys = OFF"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.DB.Exec("DROP TABLE " + tc.table); err != nil {
				t.Fatal(err)
			}
			status := requestStatus(t, f.server.URL+tc.path, tc.method, tc.body, admin)
			if status < 500 {
				t.Fatalf("%s %s with dropped %s = %d, want 5xx", tc.method, tc.path, tc.table, status)
			}
		})
	}

	userCases := []struct {
		table, method, path string
		body                any
	}{
		{"courses", http.MethodPost, "/api/v1/courses", map[string]any{"items": []any{map[string]any{"code": "C1", "name": "课程"}}}},
		{"error_reports", http.MethodPost, "/api/v1/error-reports", map[string]any{"event_id": "e1", "occurred_at": "2026-01-01T00:00:00Z", "title": "t", "message": "m"}},
		{"activity_events", http.MethodPost, "/api/v1/telemetry/events", []any{map[string]any{"event_id": "e1", "type": "app_start", "occurred_at": "2026-01-01T00:00:00Z"}}},
		{"share_codes", http.MethodPost, "/api/v1/share-codes", map[string]any{"suffix": "1", "data": map[string]any{"a": 1}}},
		{"library_cdks", http.MethodPost, "/api/v1/library/cdks/redeem", map[string]any{"code": "AAAAA1"}},
		{"resource_submissions", http.MethodPost, "/api/v1/resource-center/submissions", map[string]any{"kind": "resource", "course_code": "C1", "name": "x", "files": []any{map[string]any{"name": "x.pdf", "format": "pdf", "md5": "0123456789abcdef0123456789abcdef", "size_bytes": 10}}}},
	}
	for _, tc := range userCases {
		t.Run(tc.table+tc.method+tc.path, func(t *testing.T) {
			f := newAdminFixture(t, http.NotFoundHandler())
			user := f.headers
			if _, err := f.store.DB.Exec("PRAGMA foreign_keys = OFF"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.DB.Exec("DROP TABLE " + tc.table); err != nil {
				t.Fatal(err)
			}
			status := requestStatus(t, f.server.URL+tc.path, tc.method, tc.body, user)
			// Some payloads are rejected by validation before they reach the
			// missing table; either way the request must not succeed.
			if status < 400 {
				t.Fatalf("%s %s with dropped %s = %d, want a rejection", tc.method, tc.path, tc.table, status)
			}
		})
	}
}
