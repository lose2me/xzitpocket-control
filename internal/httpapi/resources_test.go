package httpapi

import (
	"net/http"
	"testing"
)

func TestResourceAdminCRUD(t *testing.T) {
	ts, _, adminToken := newAdminServer(t)
	adminHeaders := map[string]string{"Authorization": adminToken}

	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources", http.MethodGet, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated resource list status = %d, want %d", status, http.StatusUnauthorized)
	}
	created := requestWithHeaders(t, ts.URL+"/api/v1/admin/resources", http.MethodPost, map[string]any{"name": "期末真题", "tags": "2024", "course_code": "3007B0001"}, adminHeaders)
	id := created["id"].(string)
	if id == "" || created["course_code"] != "3007B0001" || created["has_pdf"] != false {
		t.Fatalf("resource create = %#v", created)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources", http.MethodPost, map[string]any{"name": "期末真题", "tags": "2024", "course_code": "3007B0001"}, adminHeaders); status != http.StatusConflict {
		t.Fatalf("duplicate resource status = %d, want %d", status, http.StatusConflict)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources/"+id, http.MethodPut, map[string]any{"name": "期末真题（含答案）", "tags": "2024/答案", "course_code": "3007B2106"}, adminHeaders); status != http.StatusOK {
		t.Fatalf("resource update status = %d, want %d", status, http.StatusOK)
	}
	listed := requestWithHeaders(t, ts.URL+"/api/v1/admin/resources?q=3007B2106", http.MethodGet, nil, adminHeaders)
	items := listed["items"].([]any)
	if listed["total"].(float64) != 1 || items[0].(map[string]any)["course_code"] != "3007B2106" {
		t.Fatalf("resource reassignment not visible: %#v", listed)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources/"+id+"/resource-url", http.MethodGet, nil, adminHeaders); status != http.StatusNotFound {
		t.Fatalf("resource URL without a PDF status = %d, want %d", status, http.StatusNotFound)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/resources/"+id, http.MethodDelete, nil, adminHeaders); status != http.StatusOK {
		t.Fatalf("resource delete status = %d, want %d", status, http.StatusOK)
	}
}
