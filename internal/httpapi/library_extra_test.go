package httpapi

import (
	"net/http"
	"testing"
)

func TestLibraryAdminValidationAndFilters(t *testing.T) {
	ts, headers, adminToken := newAdminServer(t)
	admin := map[string]string{"Authorization": adminToken}

	// Listing with every filter/sort combination.
	for _, query := range []string{
		"?limit=5&offset=0&status=active&sort=updated_at&order=desc",
		"?limit=abc&offset=-1&archived=1&sort=bogus&order=bogus",
		"?limit=1000&offset=9999",
	} {
		if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks"+query, http.MethodGet, nil, admin); status != http.StatusOK {
			t.Fatalf("admin banks%s = %d", query, status)
		}
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks?sort=bogus&order=bogus&q=x", http.MethodGet, nil, admin); status != http.StatusOK {
		t.Fatalf("cdk list = %d", status)
	}

	// Creating banks through the envelope rejects an empty payload.
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks", http.MethodPost, map[string]any{}, admin); status != http.StatusBadRequest {
		t.Fatalf("empty bank envelope = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks", http.MethodPost, map[string]any{"questionBank": map[string]any{"name": "题库", "questions": []any{}}}, admin); status != http.StatusCreated {
		t.Fatalf("create bank = %d", status)
	}
	created := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks", http.MethodPost, map[string]any{"questionBank": map[string]any{
		"name": "网络题库", "courseCodes": []string{}, "questions": []any{map[string]any{
			"questionNumber": 1, "type": "判断题", "title": "第1题", "questionText": "对错", "correctAnswer": "A",
			"options": []any{map[string]any{"label": "A", "text": "对"}, map[string]any{"label": "B", "text": "错"}},
		}},
	}}, admin)
	id := created["questionBank"].(map[string]any)["id"].(string)

	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+id, http.MethodPut, map[string]any{"questionBank": map[string]any{"id": "other", "name": "x"}}, admin); status != http.StatusBadRequest {
		t.Fatalf("update id mismatch = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+id, http.MethodPut, map[string]any{}, admin); status != http.StatusBadRequest {
		t.Fatalf("update empty envelope = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+id+"/status", http.MethodPatch, map[string]any{"status": "bogus"}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid bank status = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+id+"/status", http.MethodPatch, map[string]any{"status": "disabled"}, admin); status != http.StatusOK {
		t.Fatalf("bank status = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/missing/status", http.MethodPatch, map[string]any{"status": "active"}, admin); status != http.StatusNotFound {
		t.Fatalf("missing bank status = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+id+"/archive", http.MethodPost, nil, admin); status != http.StatusOK {
		t.Fatalf("bank archive = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+id+"/restore", http.MethodPost, nil, admin); status != http.StatusOK {
		t.Fatalf("bank restore = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/missing/archive", http.MethodPost, nil, admin); status != http.StatusNotFound {
		t.Fatalf("missing bank archive = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+id, http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("bank delete = %d", status)
	}

	// CDKs: single and batched creation, invalid status, missing id.
	single := requestWithHeaders(t, ts.URL+"/api/v1/admin/library-cdks", http.MethodPost, map[string]any{"count": 1}, admin)
	cdkID := single["id"].(string)
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks", http.MethodPost, "not-json", admin); status != http.StatusBadRequest {
		t.Fatalf("invalid cdk json = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks", http.MethodPost, map[string]any{"count": 5}, admin); status != http.StatusCreated {
		t.Fatalf("batch cdk = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks/"+cdkID, http.MethodPatch, map[string]any{"status": "bogus"}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid cdk status = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks/"+cdkID, http.MethodPatch, map[string]any{"status": "disabled"}, admin); status != http.StatusOK {
		t.Fatalf("cdk status = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks/missing", http.MethodPatch, map[string]any{"status": "disabled"}, admin); status != http.StatusNotFound {
		t.Fatalf("missing cdk status = %d", status)
	}

	// The app can browse published banks and preview them as an admin.
	if status := requestStatus(t, ts.URL+"/api/v1/question-banks?limit=5&sort=updated_at", http.MethodGet, nil, headers); status != http.StatusOK {
		t.Fatalf("user banks = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/question-banks/missing", http.MethodGet, nil, headers); status != http.StatusNotFound {
		t.Fatalf("user bank detail = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/question-banks", http.MethodGet, nil, admin); status != http.StatusOK {
		t.Fatalf("admin preview banks = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/question-banks", http.MethodGet, nil, map[string]string{"Authorization": "Bearer bad-token"}); status != http.StatusUnauthorized {
		t.Fatalf("bad token banks = %d", status)
	}

	// Redeeming: malformed body, unknown code and a disabled code.
	if status := requestStatus(t, ts.URL+"/api/v1/library/cdks/redeem", http.MethodPost, "not-json", headers); status != http.StatusBadRequest {
		t.Fatalf("invalid redemption json = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/library/cdks/redeem", http.MethodPost, map[string]any{"code": "SHORT"}, headers); status != http.StatusBadRequest {
		t.Fatalf("short code = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/library/cdks/redeem", http.MethodPost, map[string]any{"code": "ABCDE1"}, headers); status != http.StatusBadRequest {
		t.Fatalf("unknown code = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/library/cdks/redeem", http.MethodPost, map[string]any{"code": single["code"].(string)}, headers); status != http.StatusOK && status != http.StatusConflict && status != http.StatusBadRequest {
		t.Fatalf("default code redemption = %d", status)
	}
}
