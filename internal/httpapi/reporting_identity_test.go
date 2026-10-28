package httpapi

import (
	"net/http"
	"testing"
)

func TestReportingRequiresMatchingStudent(t *testing.T) {
	ts, headers, _ := newAdminServer(t)
	for _, tc := range []struct {
		name, student string
		status        int
	}{
		{"missing", "", http.StatusBadRequest},
		{"invalid", "other student", http.StatusBadRequest},
		{"different account", "20260002", http.StatusForbidden},
		{"matching account", "20260001", http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestHeaders := map[string]string{"Authorization": headers["Authorization"], "X-Student-ID": tc.student}
			body := map[string]any{"items": []any{map[string]string{"code": "C1", "name": "课程"}}}
			if got := requestStatus(t, ts.URL+"/api/v1/courses", http.MethodPost, body, requestHeaders); got != tc.status {
				t.Fatalf("course upload = %d, want %d", got, tc.status)
			}
		})
	}
	// Session-authenticated uploads, reports and state changes all share the
	// identity check, including methods with no JSON body.
	for _, tc := range []struct{ path, method string }{
		{"/telemetry/events", http.MethodPost},
		{"/error-reports", http.MethodPost},
		{"/share-codes", http.MethodPost},
		{"/library/cdks/redeem", http.MethodPost},
		{"/resource-center/submissions", http.MethodPost},
		{"/resource-center/submissions/ticket/complete", http.MethodPost},
		{"/resource-center/submissions/ticket", http.MethodDelete},
		{"/auth/revoke", http.MethodPost},
	} {
		got := requestStatus(t, ts.URL+"/api/v1"+tc.path, tc.method, nil, map[string]string{"Authorization": headers["Authorization"], "X-Student-ID": "20260002"})
		if got != http.StatusForbidden {
			t.Errorf("%s %s accepted another account: %d", tc.method, tc.path, got)
		}
	}
}

func TestLoginCandidateMustMatchBody(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	device := newTestDevice(t, "candidate")
	registered := register(t, ts.URL, device)
	headers := map[string]string{"Authorization": "Device " + registered["device_token"].(string), "X-Student-ID": "20260002"}
	for _, path := range []string{"/auth/login-eligibility", "/auth/assertions"} {
		if got := requestStatus(t, ts.URL+"/api/v1"+path, http.MethodPost, map[string]string{"student_id": "20260001"}, headers); got != http.StatusBadRequest {
			t.Fatalf("%s mismatched header/body = %d", path, got)
		}
	}
	if got := requestStatus(t, ts.URL+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{"Authorization": headers["Authorization"]}); got != http.StatusBadRequest {
		t.Fatalf("challenge missing candidate = %d", got)
	}
}
