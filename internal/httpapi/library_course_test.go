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
			"name": "随堂测验", "requiresCDK": false, "courseCode": "3007B0001",
			"questions": []any{question},
		},
	}, headers)
	bank := created["questionBank"].(map[string]any)
	id := bank["id"].(string)
	if bank["courseCode"] != "3007B0001" {
		t.Fatalf("created question bank courseCode = %#v, want 3007B0001", bank["courseCode"])
	}

	listed := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks?limit=10", http.MethodGet, nil, headers)
	items := listed["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["courseCode"] != "3007B0001" {
		t.Fatalf("listed question bank courseCode missing: %#v", listed)
	}

	requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks/"+id, http.MethodPut, map[string]any{
		"questionBank": map[string]any{
			"name": "随堂测验", "requiresCDK": false, "courseCode": "3007B2106",
			"questions": []any{question},
		},
	}, headers)
	detail := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks/"+id, http.MethodGet, nil, headers)
	if got := detail["questionBank"].(map[string]any)["courseCode"]; got != "3007B2106" {
		t.Fatalf("updated question bank courseCode = %#v, want 3007B2106", got)
	}
}
