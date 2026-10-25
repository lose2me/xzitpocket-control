package httpapi

import (
	"net/http"
	"testing"
)

func TestAdminSettingsEndpoints(t *testing.T) {
	ts, _, adminToken := newAdminServer(t)
	adminHeaders := map[string]string{"Authorization": adminToken}

	out := requestWithHeaders(t, ts.URL+"/api/v1/admin/settings", http.MethodGet, nil, adminHeaders)
	groups, _ := out["groups"].([]any)
	if len(groups) == 0 {
		t.Fatalf("settings view has no groups: %#v", out)
	}

	requestWithHeaders(t, ts.URL+"/api/v1/admin/settings", http.MethodPut, map[string]any{
		"values": map[string]string{"CONTROL_EVENT_RETENTION_DAYS": "45"},
	}, adminHeaders)

	if status := requestStatus(t, ts.URL+"/api/v1/admin/settings", http.MethodPut, map[string]any{
		"values": map[string]string{"CONTROL_EVENT_RETENTION_DAYS": "nope"},
	}, adminHeaders); status != http.StatusBadRequest {
		t.Fatalf("invalid settings = %d, want 400", status)
	}

	if status := requestStatus(t, ts.URL+"/api/v1/admin/settings", http.MethodGet, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET = %d, want 401", status)
	}
}
