package httpapi

import (
	"net/http"
	"testing"
)

func TestAdminPageFor(t *testing.T) {
	cases := []struct {
		path, method string
		page         string
		write        bool
	}{
		{"/admin/textbooks", http.MethodGet, "textbooks", false},
		{"/admin/textbooks/123/pdf", http.MethodPost, "textbooks", true},
		{"/admin/textbook-submissions", http.MethodGet, "textbook-submissions", false},
		{"/admin/textbook-submissions/abc/reject", http.MethodPost, "textbook-submissions", true},
		{"/admin/resource-submissions", http.MethodGet, "resource-submissions", false},
		{"/admin/resource-submissions/abc/reject", http.MethodPost, "resource-submissions", true},
		{"/admin/cdn/stats", http.MethodGet, "overview", false},
		{"/admin/overview-display", http.MethodPut, "overview", true},
		{"/admin/app/release-test", http.MethodPut, "app-release", true},
		{"/admin/share-codes/abc", http.MethodDelete, "share-codes", true},
		{"/admin/school-calendar/exceptions", http.MethodPut, "school-calendar", true},
		{"/admin/school-calendar/exceptions", http.MethodDelete, "school-calendar", true},
		{"/admin/unknown", http.MethodPost, "unassigned", true},
		{"/admin/settings", http.MethodPut, "", false},
		{"/admin/me", http.MethodGet, "", false},
		{"/admin/admins", http.MethodGet, "admins", false},
		{"/admin/admins/abc", http.MethodDelete, "admins", true},
	}
	for _, tc := range cases {
		page, write := adminPageFor(tc.path, tc.method)
		if page != tc.page || write != tc.write {
			t.Fatalf("adminPageFor(%q,%q) = (%q,%v), want (%q,%v)", tc.path, tc.method, page, write, tc.page, tc.write)
		}
	}
}

func limitedAdminHeaders(t *testing.T, baseURL, superToken, permissions string) map[string]string {
	t.Helper()
	key := "limited-admin-password"
	requestWithHeaders(t, baseURL+"/api/v1/admin/admins", http.MethodPost,
		map[string]string{"key": key, "permissions": permissions},
		map[string]string{"Authorization": superToken})
	login := requestWithHeaders(t, baseURL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": key}, nil)
	return map[string]string{"Authorization": "Bearer " + login["access_token"].(string)}
}

func TestIndependentAdminModules(t *testing.T) {
	ts, _, superToken := newAdminServer(t)
	headers := limitedAdminHeaders(t, ts.URL, superToken, "school-calendar=r,app-release=rw,textbook-submissions=rw,resources=r")
	for _, tc := range []struct {
		path, method string
		status       int
	}{
		{"/admin/school-calendar", http.MethodGet, http.StatusOK},
		{"/admin/school-calendar", http.MethodPut, http.StatusForbidden},
		{"/admin/school-calendar/exceptions", http.MethodPut, http.StatusForbidden},
		{"/admin/school-calendar/exceptions", http.MethodDelete, http.StatusForbidden},
		{"/admin/app/release", http.MethodPut, http.StatusOK},
		{"/admin/textbook-submissions", http.MethodGet, http.StatusOK},
		{"/admin/textbooks", http.MethodPost, http.StatusForbidden},
		{"/admin/resource-submissions", http.MethodGet, http.StatusForbidden},
		{"/admin/resources", http.MethodGet, http.StatusOK},
		{"/admin/resources", http.MethodPost, http.StatusForbidden},
		{"/admin/courses", http.MethodGet, http.StatusOK},
		{"/admin/courses", http.MethodPost, http.StatusForbidden},
		{"/admin/admins", http.MethodGet, http.StatusForbidden},
		{"/admin/admins", http.MethodPost, http.StatusForbidden},
		{"/admin/admins/other", http.MethodDelete, http.StatusForbidden},
		{"/admin/unknown", http.MethodPut, http.StatusForbidden},
	} {
		body := map[string]string{}
		if tc.path == "/admin/app/release" {
			body = map[string]string{"latestVersion": "2.1.0", "downloadUrl": "https://example.test/app.apk", "changelog": "test"}
		}
		if got := requestStatus(t, ts.URL+"/api/v1"+tc.path, tc.method, body, headers); got != tc.status {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, got, tc.status)
		}
	}
}

func TestSettingsPermissionsFilterAndRejectMixedWrites(t *testing.T) {
	ts, _, superToken := newAdminServer(t)
	headers := limitedAdminHeaders(t, ts.URL, superToken, "settings-general=rw,settings-ai=r")
	read := func() map[string]map[string]any {
		out := requestWithHeaders(t, ts.URL+"/api/v1/admin/settings", http.MethodGet, nil, headers)
		fields := map[string]map[string]any{}
		for _, rawGroup := range out["groups"].([]any) {
			for _, rawField := range rawGroup.(map[string]any)["fields"].([]any) {
				field := rawField.(map[string]any)
				fields[field["key"].(string)] = field
			}
		}
		return fields
	}
	fields := read()
	if fields["CONTROL_AI_MODEL"] == nil || fields["CONTROL_EVENT_RETENTION_DAYS"] == nil || fields["CONTROL_COS_BUCKET"] != nil || fields["CONTROL_OVERVIEW_SCALE_PERCENT"] != nil {
		t.Fatalf("settings not filtered by module: %#v", fields)
	}
	before := fields["CONTROL_EVENT_RETENTION_DAYS"]["value"]
	for _, body := range []map[string]any{
		{"values": map[string]string{"CONTROL_EVENT_RETENTION_DAYS": "45", "CONTROL_AI_MODEL": "unauthorized"}},
		{"values": map[string]string{"CONTROL_EVENT_RETENTION_DAYS": "45"}, "clear": []string{"CONTROL_AI_API_KEY"}},
		{"values": map[string]string{"CONTROL_OVERVIEW_SCALE_PERCENT": "200"}},
		{"values": map[string]string{"unknown": "1"}},
	} {
		if got := requestStatus(t, ts.URL+"/api/v1/admin/settings", http.MethodPut, body, headers); got != http.StatusForbidden {
			t.Fatalf("unauthorized settings write = %d", got)
		}
		if read()["CONTROL_EVENT_RETENTION_DAYS"]["value"] != before {
			t.Fatal("mixed write partially changed an allowed field")
		}
	}
	requestWithHeaders(t, ts.URL+"/api/v1/admin/settings", http.MethodPut, map[string]any{"values": map[string]string{"CONTROL_EVENT_RETENTION_DAYS": "45"}}, headers)
	if got := read()["CONTROL_EVENT_RETENTION_DAYS"]["value"]; got != "45" {
		t.Fatalf("authorized settings write did not persist: %v", got)
	}
}
