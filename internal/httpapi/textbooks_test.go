package httpapi

import (
	"net/http"
	"testing"
)

func TestTextbookAdminCRUD(t *testing.T) {
	ts, _, adminToken := newAdminServer(t)
	adminHeaders := map[string]string{"Authorization": adminToken}

	if status := requestStatus(t, ts.URL+"/api/v1/admin/textbooks", http.MethodGet, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated textbook list status = %d, want %d", status, http.StatusUnauthorized)
	}
	created := requestWithHeaders(t, ts.URL+"/api/v1/admin/textbooks", http.MethodPost, map[string]any{"name": "高等数学", "tags": "高教社", "course_codes": []string{"3007B0001", "3007B2106"}}, adminHeaders)
	id := created["id"].(string)
	if id == "" || created["course_code"] != "3007B0001" || len(created["course_codes"].([]any)) != 2 {
		t.Fatalf("textbook create = %#v", created)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/textbooks", http.MethodPost, map[string]any{"name": "高等数学", "tags": "高教社", "course_codes": []string{"3007B0001"}}, adminHeaders); status != http.StatusConflict {
		t.Fatalf("duplicate textbook status = %d, want %d", status, http.StatusConflict)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/textbooks/"+id, http.MethodPut, map[string]any{"name": "高等数学（上）", "tags": "高教社/第7版", "course_codes": []string{"3007X0001", "3007B2106"}}, adminHeaders); status != http.StatusOK {
		t.Fatalf("textbook update status = %d, want %d", status, http.StatusOK)
	}
	listed := requestWithHeaders(t, ts.URL+"/api/v1/admin/textbooks?q=3007X0001", http.MethodGet, nil, adminHeaders)
	items := listed["items"].([]any)
	if listed["total"].(float64) != 1 || items[0].(map[string]any)["course_code"] != "3007X0001" || len(items[0].(map[string]any)["course_codes"].([]any)) != 2 {
		t.Fatalf("textbook reassignment not visible: %#v", listed)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/textbooks/"+id, http.MethodDelete, nil, adminHeaders); status != http.StatusOK {
		t.Fatalf("textbook delete status = %d, want %d", status, http.StatusOK)
	}
}
