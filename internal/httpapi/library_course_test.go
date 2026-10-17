package httpapi

import (
	"net/http"
	"testing"
)

func TestQuestionBankAdminCourseCode(t *testing.T) {
	ts, _, adminToken := newAdminServer(t)
	headers := map[string]string{"Authorization": adminToken}
	question := map[string]any{"questionNumber": 1, "type": "填空题", "title": "题", "questionText": "题干", "correctAnswer": "答案"}

	created := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks", http.MethodPost, map[string]any{
		"questionBank": map[string]any{
			"name": "随堂测验", "requiresCDK": false, "courseCodes": []string{"3007B0001", "3007B2106"},
			"questions": []any{question},
		},
	}, headers)
	bank := created["questionBank"].(map[string]any)
	id := bank["id"].(string)
	if bank["courseCode"] != "3007B0001" || len(bank["courseCodes"].([]any)) != 2 {
		t.Fatalf("created question bank courseCodes = %#v", bank)
	}

	listed := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks?limit=10", http.MethodGet, nil, headers)
	items := listed["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["courseCode"] != "3007B0001" || len(items[0].(map[string]any)["courseCodes"].([]any)) != 2 {
		t.Fatalf("listed question bank courseCodes missing: %#v", listed)
	}

	requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks/"+id, http.MethodPut, map[string]any{
		"questionBank": map[string]any{
			"name": "随堂测验", "requiresCDK": false, "courseCodes": []string{"3007B2106"},
			"questions": []any{question},
		},
	}, headers)
	detail := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks/"+id, http.MethodGet, nil, headers)
	if got := detail["questionBank"].(map[string]any)["courseCode"]; got != "3007B2106" {
		t.Fatalf("updated question bank courseCode = %#v, want 3007B2106", got)
	}
	if got := detail["questionBank"].(map[string]any)["courseCodes"]; len(got.([]any)) != 1 {
		t.Fatalf("updated question bank courseCodes = %#v, want one course", got)
	}
}
