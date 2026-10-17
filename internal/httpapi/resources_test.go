package httpapi

import (
	"net/http"
	"testing"
)

func TestResourceRoutes(t *testing.T) {
	ts, _, adminToken := newAdminServer(t)
	adminHeaders := map[string]string{"Authorization": adminToken}

	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources", http.MethodGet, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated resource list status = %d, want %d", status, http.StatusUnauthorized)
	}
	listed := requestWithHeaders(t, ts.URL+"/api/v1/admin/resources?course_code=C1", http.MethodGet, nil, adminHeaders)
	if listed["total"].(float64) != 0 {
		t.Fatalf("empty resource list = %#v", listed)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources/upload", http.MethodPost, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upload status = %d, want %d", status, http.StatusUnauthorized)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources/upload", http.MethodPost, nil, adminHeaders); status != http.StatusBadRequest {
		t.Fatalf("upload without files status = %d, want %d", status, http.StatusBadRequest)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources/missing/resource-url", http.MethodGet, nil, adminHeaders); status != http.StatusNotFound {
		t.Fatalf("resource-url for missing id status = %d, want %d", status, http.StatusNotFound)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources/missing", http.MethodDelete, nil, adminHeaders); status != http.StatusNotFound {
		t.Fatalf("delete missing resource status = %d, want %d", status, http.StatusNotFound)
	}
}
