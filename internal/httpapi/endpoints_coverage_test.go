package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func multipartBody(t *testing.T, fields map[string]string, fileField, filename string, data []byte) (io.Reader, string) {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer, writer.FormDataContentType()
}

func sendMultipart(t *testing.T, endpoint string, fields map[string]string, fileField, filename string, data []byte, headers map[string]string) int {
	t.Helper()
	body, contentType := multipartBody(t, fields, fileField, filename, data)
	req, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func rawRequest(t *testing.T, endpoint, method, body string, headers map[string]string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

func TestServerProtocolAndStaticAssets(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("asset"))
	})
	f := newAdminFixture(t, static)
	admin := map[string]string{"Authorization": f.token}

	status, headers, body := rawRequest(t, f.server.URL+"/healthz", http.MethodGet, "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"status":"ok"`) || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("healthz = %d %q %v", status, body, headers)
	}
	status, headers, _ = rawRequest(t, f.server.URL+"/api/v1/admin/admins", http.MethodOptions, "", nil)
	if status != http.StatusNoContent || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("options = %d %v", status, headers)
	}
	status, headers, body = rawRequest(t, f.server.URL+"/index.html", http.MethodGet, "", nil)
	if status != http.StatusOK || body != "asset" || headers.Get("Cache-Control") != "no-cache" {
		t.Fatalf("static = %d %q %v", status, body, headers)
	}
	status, headers, _ = rawRequest(t, f.server.URL+"/vendor/js/app.js", http.MethodGet, "", nil)
	if status != http.StatusOK || !strings.Contains(headers.Get("Cache-Control"), "immutable") {
		t.Fatalf("vendor = %d %v", status, headers)
	}
	if status, _, _ := rawRequest(t, f.server.URL+"/api/v1/nope", http.MethodGet, "", nil); status != http.StatusNotFound {
		t.Fatalf("unknown api = %d", status)
	}
	if status, _, _ := rawRequest(t, f.server.URL+"/api/v1/admin/question-banks", http.MethodPost, "not-json", admin); status != http.StatusBadRequest {
		t.Fatalf("invalid json = %d", status)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := rawRequest(t, f.server.URL+"/healthz", http.MethodGet, "", nil); status != http.StatusServiceUnavailable {
		t.Fatalf("closed database healthz = %d", status)
	}
}

func TestAdminOverviewAndStatsEndpoints(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	for _, path := range []string{
		"/api/v1/admin/metrics/overview", "/api/v1/admin/metrics/series?days=7", "/api/v1/admin/metrics/breakdown",
		"/api/v1/admin/users", "/api/v1/admin/users/user", "/api/v1/admin/devices",
		"/api/v1/admin/risk-events", "/api/v1/admin/audit", "/api/v1/admin/error-reports",
		"/api/v1/admin/app/release", "/api/v1/admin/app/release-test", "/api/v1/admin/settings",
		"/api/v1/admin/school-calendar", "/api/v1/admin/overview-display",
		"/api/v1/admin/question-banks", "/api/v1/admin/library-cdks", "/api/v1/admin/share-codes",
		"/api/v1/admin/textbooks", "/api/v1/admin/textbook-submissions", "/api/v1/admin/resource-submissions",
		"/api/v1/admin/resources", "/api/v1/admin/ai-analyses", "/api/v1/admin/courses", "/api/v1/admin/admins",
	} {
		if status := requestStatus(t, f.server.URL+path, http.MethodGet, nil, admin); status != http.StatusOK {
			t.Fatalf("%s status = %d", path, status)
		}
	}
	// Stats endpoints need cloud credentials.
	for _, path := range []string{"/api/v1/admin/cdn/stats?days=7", "/api/v1/admin/cdn/stats?days=abc", "/api/v1/admin/host/stats", "/api/v1/admin/buckets/stats?refresh=1"} {
		if status := requestStatus(t, f.server.URL+path, http.MethodGet, nil, admin); status != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want unavailable", path, status)
		}
	}
	updated := requestWithHeaders(t, f.server.URL+"/api/v1/admin/overview-display", http.MethodPut, map[string]any{"scalePercent": 250}, admin)
	if updated["scalePercent"].(float64) != 250 {
		t.Fatalf("scale = %#v", updated)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/overview-display", http.MethodPut, map[string]any{"scalePercent": "x"}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid scale = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/config/versions", http.MethodGet, nil, nil); status != http.StatusOK {
		t.Fatalf("config versions = %d", status)
	}
}

func TestAdminUserDeviceRiskAndErrorReports(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	ctx := context.Background()
	if err := f.store.SetUserStatus(ctx, "user", "disabled"); err != nil {
		t.Fatal(err)
	}
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/users/user/status", http.MethodPatch, map[string]any{"status": "active"}, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/users/user/status", http.MethodPatch, map[string]any{"status": "bogus"}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid user status = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/users/missing", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("missing user = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/devices/device/status", http.MethodPatch, map[string]any{"status": "revoked"}, admin); status != http.StatusOK {
		t.Fatalf("device revoke = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/devices/missing/status", http.MethodPatch, map[string]any{"status": "revoked"}, admin); status != http.StatusNotFound {
		t.Fatalf("missing device = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/devices/device/status", http.MethodPatch, map[string]any{"status": "bogus"}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid device status = %d", status)
	}

	if err := f.store.CreateRiskEvent(ctx, sqlite.RiskEvent{ID: "risk-1", Type: "login_attempt_burst", UserID: "user", DeviceID: "device", ObservedCount: 3, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	risks := requestWithHeaders(t, f.server.URL+"/api/v1/admin/risk-events?limit=10", http.MethodGet, nil, admin)
	items := risks["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("risk list = %#v", risks)
	}
	id := items[0].(map[string]any)["id"].(string)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/risk-events/"+id, http.MethodPatch, map[string]any{}, admin); status != http.StatusOK {
		t.Fatalf("acknowledge = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/risk-events/missing", http.MethodPatch, map[string]any{}, admin); status != http.StatusNotFound {
		t.Fatalf("acknowledge missing = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/risk-events", http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("clear risk = %d", status)
	}

	if _, err := f.store.InsertErrorReport(ctx, sqlite.ErrorReportInput{EventID: "e1", UserID: "user", DeviceID: "device", StudentIDHash: "hash", Title: "t", Message: "m", Platform: "android", AppVersion: "1", OccurredAt: time.Now().UTC(), ReceivedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	reports := requestWithHeaders(t, f.server.URL+"/api/v1/admin/error-reports", http.MethodGet, nil, admin)
	reportItems := reports["items"].([]any)
	reportID := int64(reportItems[0].(map[string]any)["id"].(float64))
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/error-reports/"+itoa64(reportID), http.MethodPatch, map[string]any{"ignored": true}, admin); status != http.StatusOK {
		t.Fatalf("ignore report = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/error-reports/not-a-number", http.MethodPatch, map[string]any{"ignored": true}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid report id = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/error-reports", http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("clear reports = %d", status)
	}
}

func itoa64(value int64) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(jsonNumber(value), "\"", ""), " ", ""))
}

func jsonNumber(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestAdminAppReleaseAndSettings(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	created := requestWithHeaders(t, f.server.URL+"/api/v1/admin/app/release", http.MethodPut, map[string]any{
		"latestVersion": "2.0.0", "downloadUrl": "https://dl.xuda.live/app.apk", "changelog": "更新",
	}, admin)
	if created["latestVersion"] != "2.0.0" {
		t.Fatalf("release = %#v", created)
	}
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/app/release-test", http.MethodPut, map[string]any{
		"latestVersion": "2.1.0-test", "downloadUrl": "https://dl.xuda.live/test.apk", "changelog": "测试",
	}, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/app/release", http.MethodPut, map[string]any{"latestVersion": ""}, admin); status != http.StatusBadRequest {
		t.Fatalf("missing version = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/app/release", http.MethodPut, map[string]any{"latestVersion": "3", "downloadUrl": ""}, admin); status != http.StatusBadRequest {
		t.Fatalf("missing url = %d", status)
	}

	settings := requestWithHeaders(t, f.server.URL+"/api/v1/admin/settings", http.MethodGet, nil, admin)
	groups := settings["groups"].([]any)
	if len(groups) == 0 {
		t.Fatal("settings groups empty")
	}
	if status, _, body := rawRequest(t, f.server.URL+"/api/v1/admin/settings", http.MethodPut, `{"values":{"CONTROL_UNKNOWN":"1"}}`, admin); status != http.StatusForbidden {
		t.Fatalf("unknown setting = %d %s", status, body)
	}
	saved := requestWithHeaders(t, f.server.URL+"/api/v1/admin/settings", http.MethodPut, map[string]any{"values": map[string]string{"CONTROL_OVERVIEW_SCALE_PERCENT": "150"}}, admin)
	if len(saved["groups"].([]any)) == 0 {
		t.Fatalf("settings save = %#v", saved)
	}
}

func TestAdminSchoolCalendar(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	saved := requestWithHeaders(t, f.server.URL+"/api/v1/admin/school-calendar", http.MethodPut, map[string]any{
		"days":  []any{map[string]any{"date": "2026-09-01", "name": "开学", "adjustment": ""}, map[string]any{"date": "2026-09-02", "name": "", "adjustment": "20260905"}},
		"slots": []any{map[string]any{"index": 1, "start": "08:00", "end": "08:45", "lab": false}},
	}, admin)
	if len(saved["days"].([]any)) != 2 {
		t.Fatalf("calendar save = %#v", saved)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/school-calendar", http.MethodPut, map[string]any{"days": []any{}}, admin); status != http.StatusBadRequest {
		t.Fatalf("empty calendar = %d", status)
	}
	exceptions := requestWithHeaders(t, f.server.URL+"/api/v1/admin/school-calendar/exceptions", http.MethodPut, map[string]any{
		"college": "", "exceptions": []any{map[string]any{"date": "2026-09-01", "name": "调整"}},
	}, admin)
	if len(exceptions["colleges"].([]any)) != 0 {
		t.Fatalf("global exceptions = %#v", exceptions)
	}
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/school-calendar/exceptions", http.MethodPut, map[string]any{
		"college": "计算机学院", "exceptions": []any{map[string]any{"date": "2026-09-02", "name": "院庆"}},
	}, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/school-calendar/exceptions?college="+urlQuery("计算机学院"), http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("delete college exceptions = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/school-calendar/exceptions", http.MethodDelete, nil, admin); status != http.StatusBadRequest {
		t.Fatalf("delete global exceptions = %d", status)
	}
	user := f.headers
	if status := requestStatus(t, f.server.URL+"/api/v1/school-calendar", http.MethodGet, nil, user); status != http.StatusOK {
		t.Fatalf("user calendar = %d", status)
	}
}

func urlQuery(value string) string {
	var out strings.Builder
	for _, b := range []byte(value) {
		out.WriteString("%")
		out.WriteString(strings.ToUpper(jsonNumber(int64(b))[1:]))
	}
	return out.String()
}

func TestAdminQuestionBankAndCDK(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	created := requestWithHeaders(t, f.server.URL+"/api/v1/admin/question-banks", http.MethodPost, map[string]any{
		"questionBank": map[string]any{
			"name": "题库", "courseCodes": []string{"C1"}, "requiresCDK": false,
			"questions": []any{map[string]any{"questionNumber": 1, "type": "单选题", "title": "第1题", "questionText": "1+1", "correctAnswer": "A", "options": []any{map[string]any{"label": "A", "text": "2"}}}},
		},
	}, admin)
	bankID := created["questionBank"].(map[string]any)["id"].(string)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/question-banks", http.MethodPost, map[string]any{"questionBank": map[string]any{"name": ""}}, admin); status != http.StatusBadRequest {
		t.Fatalf("empty bank name = %d", status)
	}
	detail := requestWithHeaders(t, f.server.URL+"/api/v1/admin/question-banks/"+bankID, http.MethodGet, nil, admin)
	if detail["questionBank"].(map[string]any)["name"] != "题库" {
		t.Fatalf("bank detail = %#v", detail)
	}
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/question-banks/"+bankID, http.MethodPut, map[string]any{
		"questionBank": map[string]any{"name": "题库二", "status": "active", "requiresCDK": true, "courseCodes": []string{"C1"}, "questions": []any{}},
	}, admin)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/question-banks/"+bankID+"/status", http.MethodPatch, map[string]any{"status": "disabled"}, admin)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/question-banks/"+bankID+"/archive", http.MethodPost, nil, admin)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/question-banks/"+bankID+"/restore", http.MethodPost, nil, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/question-banks/missing", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("missing bank = %d", status)
	}

	cdks := requestWithHeaders(t, f.server.URL+"/api/v1/admin/library-cdks", http.MethodPost, map[string]any{"count": 3}, admin)
	items := cdks["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("cdk create = %#v", cdks)
	}
	cdkID := items[0].(map[string]any)["id"].(string)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/library-cdks/"+cdkID, http.MethodPatch, map[string]any{"status": "disabled"}, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/library-cdks", http.MethodPost, map[string]any{"count": 0}, admin); status != http.StatusCreated {
		t.Fatalf("cdk zero count should clamp = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/library-cdks/"+cdkID, http.MethodPatch, map[string]any{"status": "bogus"}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid cdk status = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/question-banks/"+bankID, http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("delete bank = %d", status)
	}
}

func TestAdminShareCodes(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	created := requestWithHeaders(t, f.server.URL+"/api/v1/admin/share-codes", http.MethodPost, map[string]any{
		"suffix": "2", "data": map[string]any{"courses": []any{map[string]any{"name": "高数"}}},
	}, admin)
	code := created["code"].(string)
	if len(code) != 6 {
		t.Fatalf("share code = %q", code)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/share-codes", http.MethodPost, map[string]any{"suffix": "9", "data": map[string]any{}}, admin); status != http.StatusBadRequest {
		t.Fatalf("invalid suffix = %d", status)
	}
	listed := requestWithHeaders(t, f.server.URL+"/api/v1/admin/share-codes", http.MethodGet, nil, admin)
	shareID := listed["items"].([]any)[0].(map[string]any)["id"].(string)
	detail := requestWithHeaders(t, f.server.URL+"/api/v1/admin/share-codes/"+shareID, http.MethodGet, nil, admin)
	if detail["shareCode"] == nil {
		t.Fatalf("share code detail = %#v", detail)
	}
	read := requestWithHeaders(t, f.server.URL+"/api/v1/share-codes/"+code, http.MethodGet, nil, nil)
	if read["data"] == nil {
		t.Fatalf("share code read = %#v", read)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/share-codes/ABCDE1", http.MethodGet, nil, nil); status != http.StatusNotFound {
		t.Fatalf("missing share code = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/share-codes/"+shareID, http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("delete share code = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/share-codes/missing", http.MethodDelete, nil, admin); status != http.StatusNotFound {
		t.Fatalf("delete missing share code = %d", status)
	}
}

func TestAdminTextbooksAndSubmissions(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	created := requestWithHeaders(t, f.server.URL+"/api/v1/admin/textbooks", http.MethodPost, map[string]any{"name": "教材", "tags": "高教", "course_codes": []string{"C1"}}, admin)
	id := created["id"].(string)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/textbooks/"+id, http.MethodPut, map[string]any{"name": "教材二", "tags": "", "course_codes": []string{"C1"}}, admin)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/contributor", http.MethodPost, map[string]any{"student_id": "20260001"}, admin)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/archive", http.MethodPost, nil, admin)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/restore", http.MethodPost, nil, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/resource-url?kind=cover", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("textbook cover url without object = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/resource-url", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("textbook url without pdf = %d", status)
	}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/pdf", nil, "file", "book.pdf", []byte("%PDF-1.4"), admin); status != http.StatusServiceUnavailable {
		t.Fatalf("textbook pdf upload without storage = %d", status)
	}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/pdf", nil, "", "", nil, admin); status != http.StatusBadRequest {
		t.Fatalf("textbook pdf upload without file = %d", status)
	}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/textbooks/"+id+"/cover", nil, "", "", nil, admin); status != http.StatusServiceUnavailable {
		t.Fatalf("textbook cover without storage = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/textbooks/missing", http.MethodDelete, nil, admin); status != http.StatusNotFound {
		t.Fatalf("delete missing textbook = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/textbooks/"+id, http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("delete textbook = %d", status)
	}

	if status := requestStatus(t, f.server.URL+"/api/v1/admin/textbook-submissions/missing/reject", http.MethodPost, nil, admin); status != http.StatusNotFound {
		t.Fatalf("reject missing textbook submission = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resource-submissions/missing/reject", http.MethodPost, nil, admin); status != http.StatusNotFound {
		t.Fatalf("reject missing resource submission = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/textbook-submissions/missing/file?file=f1", http.MethodDelete, nil, admin); status != http.StatusNotFound {
		t.Fatalf("delete missing submission file = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resource-submissions/missing/file?file=f1", http.MethodDelete, nil, admin); status != http.StatusNotFound {
		t.Fatalf("delete missing resource file = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/textbook-submissions/missing/download", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("download missing submission = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resource-submissions/missing/resource-url", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("submission url missing = %d", status)
	}
}

func TestAdminResourcesAndUploads(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/resources/upload", map[string]string{"course_code": "C1"}, "files", "试卷.pdf", []byte("%PDF-1.4"), admin); status != http.StatusServiceUnavailable {
		t.Fatalf("resource upload without storage = %d", status)
	}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/resources/upload", map[string]string{"course_code": ""}, "files", "试卷.pdf", []byte("%PDF-1.4"), admin); status != http.StatusBadRequest {
		t.Fatalf("resource upload without course = %d", status)
	}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/resources/upload", map[string]string{"course_code": "C1"}, "", "", nil, admin); status != http.StatusBadRequest {
		t.Fatalf("resource upload without files = %d", status)
	}
	now := time.Now().UTC()
	resource, err := f.store.CreateResource(context.Background(), sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID+"/archive", http.MethodPost, nil, admin); status != http.StatusOK {
		t.Fatalf("archive resource = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID+"/restore", http.MethodPost, nil, admin); status != http.StatusOK {
		t.Fatalf("restore resource = %d", status)
	}
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID+"/contributor", http.MethodPost, map[string]any{"student_id": ""}, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID+"/download", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("resource download without object = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID+"/resource-url", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("resource url without object = %d", status)
	}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID+"/manual-analysis", nil, "file", "manual.pdf", []byte("%PDF-1.4"), admin); status != http.StatusServiceUnavailable {
		t.Fatalf("manual analysis without storage = %d", status)
	}
	if status := sendMultipart(t, f.server.URL+"/api/v1/admin/resources/missing/manual-analysis", nil, "", "", nil, admin); status != http.StatusBadRequest {
		t.Fatalf("manual analysis without file = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID, http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("delete resource = %d", status)
	}
}

func TestAdminAIAnalyses(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	now := time.Now().UTC()
	resource, err := f.store.CreateResource(context.Background(), sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/ai-analyses", http.MethodPost, map[string]any{"resource_ids": []string{resource.ID}}, admin); status != http.StatusServiceUnavailable {
		t.Fatalf("enqueue with AI disabled = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/ai-analyses", http.MethodPost, "not-json", admin); status != http.StatusBadRequest {
		t.Fatalf("enqueue invalid json = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/ai-analyses/missing", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("missing ai task = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/ai-analyses/missing/retry", http.MethodPost, nil, admin); status != http.StatusNotFound {
		t.Fatalf("retry missing ai task = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/ai-analyses/missing/cancel", http.MethodPost, nil, admin); status != http.StatusNotFound {
		t.Fatalf("cancel missing ai task = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/ai-analyses/missing/download?mode=basic", http.MethodGet, nil, admin); status != http.StatusNotFound {
		t.Fatalf("download missing ai task = %d", status)
	}
}

func TestAdminCoursesAndAdmins(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	created := requestWithHeaders(t, f.server.URL+"/api/v1/admin/courses", http.MethodPost, map[string]any{"code": "C9", "name": "课程九"}, admin)
	id := created["id"].(string)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/courses/"+id, http.MethodPut, map[string]any{"code": "C9", "name": "课程九（改）"}, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/courses", http.MethodPost, map[string]any{"code": "", "name": "x"}, admin); status != http.StatusBadRequest {
		t.Fatalf("course without code = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/courses/missing", http.MethodPut, map[string]any{"code": "C1", "name": "x"}, admin); status != http.StatusNotFound {
		t.Fatalf("update missing course = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/courses/"+id, http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("delete course = %d", status)
	}

	adminCreated := requestWithHeaders(t, f.server.URL+"/api/v1/admin/admins", http.MethodPost, map[string]any{"name": "operator", "key": "admin-key-123", "permissions": "users=r"}, admin)
	adminID := adminCreated["id"].(string)
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/admins/"+adminID, http.MethodPut, map[string]any{"name": "operator2", "permissions": "users=rw"}, admin)
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/admins", http.MethodPost, map[string]any{"name": "", "key": "short", "permissions": "users=r"}, admin); status != http.StatusBadRequest {
		t.Fatalf("short admin key = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/admins/"+adminID, http.MethodDelete, nil, admin); status != http.StatusOK {
		t.Fatalf("delete admin = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/admins", http.MethodGet, nil, map[string]string{"Authorization": "Bearer access"}); status != http.StatusUnauthorized {
		t.Fatalf("non-admin token = %d", status)
	}
}

func TestAdminPermissionDeniedForModule(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	admin := map[string]string{"Authorization": f.token}
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/admins", http.MethodPost, map[string]any{"name": "reader", "key": "readonly-key-1", "permissions": "users=r"}, admin)
	login := requestWithHeaders(t, f.server.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "readonly-key-1"}, nil)
	reader := map[string]string{"Authorization": "Bearer " + login["access_token"].(string)}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/users", http.MethodGet, nil, reader); status != http.StatusOK {
		t.Fatalf("read allowed = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/users/user/status", http.MethodPatch, map[string]any{"status": "active"}, reader); status != http.StatusForbidden {
		t.Fatalf("write denied = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/audit", http.MethodGet, nil, reader); status != http.StatusForbidden {
		t.Fatalf("other module denied = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/courses", http.MethodGet, nil, reader); status != http.StatusForbidden {
		t.Fatalf("course catalogue denied = %d", status)
	}
	// Content editors can read the shared course catalogue.
	requestWithHeaders(t, f.server.URL+"/api/v1/admin/admins", http.MethodPost, map[string]any{"name": "editor", "key": "editor-key-123", "permissions": "textbooks=r"}, admin)
	editorLogin := requestWithHeaders(t, f.server.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "editor-key-123"}, nil)
	editor := map[string]string{"Authorization": "Bearer " + editorLogin["access_token"].(string)}
	courses := requestWithHeaders(t, f.server.URL+"/api/v1/admin/courses", http.MethodGet, nil, editor)
	if courses["items"] == nil {
		t.Fatalf("course catalogue read = %#v", courses)
	}
}

func TestUserResourceCenterEndpoints(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	user := f.headers
	for _, path := range []string{
		"/api/v1/me", "/api/v1/me/devices", "/api/v1/app/release", "/api/v1/app/release-test",
		"/api/v1/config/versions", "/api/v1/school-calendar", "/api/v1/resource-center/courses",
		"/api/v1/resource-center/upload-stats", "/api/v1/resource-center/upload-textbooks",
		"/api/v1/resources?course_code=C1", "/api/v1/textbooks?course_code=C1", "/api/v1/question-banks",
	} {
		if status := requestStatus(t, f.server.URL+path, http.MethodGet, nil, user); status != http.StatusOK {
			t.Fatalf("%s status = %d", path, status)
		}
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resources/missing/ai", http.MethodGet, nil, user); status != http.StatusNotFound {
		t.Fatalf("missing resource ai = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resources/missing/url", http.MethodGet, nil, user); status != http.StatusNotFound {
		t.Fatalf("missing resource url = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/textbooks/missing/url", http.MethodGet, nil, user); status != http.StatusNotFound {
		t.Fatalf("missing textbook url = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/question-banks/missing", http.MethodGet, nil, user); status != http.StatusNotFound {
		t.Fatalf("missing question bank = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/library/cdks/redeem", http.MethodPost, map[string]any{"code": "nope"}, user); status != http.StatusBadRequest {
		t.Fatalf("redeem missing cdk = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resource-center/submissions", http.MethodPost, map[string]any{"kind": "resource", "course_code": "C1", "files": []any{}}, user); status != http.StatusBadRequest {
		t.Fatalf("submission ticket without files = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resource-center/submissions/missing/complete", http.MethodPost, nil, user); status != http.StatusNotFound {
		t.Fatalf("complete missing ticket = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resource-center/submissions/missing", http.MethodDelete, nil, user); status != http.StatusNotFound {
		t.Fatalf("abort missing ticket = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resource-center/submissions", http.MethodPost, map[string]any{"kind": "bogus", "course_code": "C1", "files": []any{map[string]any{"name": "a.pdf", "size": 10}}}, user); status != http.StatusBadRequest {
		t.Fatalf("submission ticket with invalid kind = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/share-codes", http.MethodPost, map[string]any{"suffix": "1", "data": map[string]any{"weeks": []int{1}}}, user); status != http.StatusCreated {
		t.Fatalf("user share code = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/error-reports", http.MethodPost, map[string]any{"event_id": "e2", "occurred_at": time.Now().UTC().Format(time.RFC3339), "title": "崩溃", "message": "详情"}, user); status != http.StatusAccepted && status != http.StatusCreated {
		t.Fatalf("error report = %d", status)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/telemetry/events", http.MethodPost, []any{map[string]any{"event_id": "ev2", "type": "app_start", "occurred_at": time.Now().UTC().Format(time.RFC3339)}}, user); status != http.StatusAccepted && status != http.StatusOK {
		t.Fatalf("telemetry = %d", status)
	}
}

func TestRateLimitedEndpoints(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	user := f.headers
	status := 0
	for i := 0; i < 31; i++ {
		status = requestStatus(t, f.server.URL+"/api/v1/share-codes", http.MethodPost, map[string]any{"suffix": "1", "data": map[string]any{"i": i}}, user)
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("share-code rate limit = %d", status)
	}
	limited := requestWithHeaders(t, f.server.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "test-password"}, nil)
	if limited["access_token"] == nil {
		t.Fatalf("admin login = %#v", limited)
	}
}

func TestAuthenticateSessionRejectsBadTokens(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	for _, headers := range []map[string]string{
		nil,
		{"Authorization": "Bearer unknown"},
	} {
		if status := requestStatus(t, f.server.URL+"/api/v1/me", http.MethodGet, nil, headers); status != http.StatusUnauthorized {
			t.Fatalf("me status = %d", status)
		}
	}
	cookieReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	cookieReq.AddCookie(&http.Cookie{Name: "control_access", Value: "unknown"})
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, cookieReq)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("cookie token = %d", recorder.Code)
	}
}

func TestResourceCenterDownloadContributionGate(t *testing.T) {
	f := newAdminFixture(t, http.NotFoundHandler())
	ctx := context.Background()
	now := time.Now().UTC()
	resource, err := f.store.CreateResource(ctx, sqlite.ResourceInput{Name: "试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	textbook, err := f.store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "教材", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.app.Cfg
	cfg.DownloadMinContributions = 1
	f.app.SetConfigForTesting(cfg)

	for _, path := range []string{
		"/api/v1/resources/" + resource.ID + "/url",
		"/api/v1/textbooks/" + textbook.ID + "/url",
		"/api/v1/resources/" + resource.ID + "/ai?mode=basic",
	} {
		if status := requestStatus(t, f.server.URL+path, http.MethodGet, nil, f.headers); status != http.StatusForbidden {
			t.Fatalf("%s below the contribution threshold = %d, want 403", path, status)
		}
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resources/"+resource.ID+"/url", http.MethodGet, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated download = %d", status)
	}
	// Admin downloads are not gated by the app policy.
	if status := requestStatus(t, f.server.URL+"/api/v1/admin/resources/"+resource.ID+"/download", http.MethodGet, nil, map[string]string{"Authorization": f.token}); status == http.StatusForbidden {
		t.Fatal("admin download should not be blocked by the app gate")
	}

	// One credited resource unlocks the app downloads.
	if err := f.store.SetResourceContributor(ctx, resource.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resources/"+resource.ID+"/url", http.MethodGet, nil, f.headers); status == http.StatusForbidden {
		t.Fatal("resource download should pass the gate")
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/textbooks/"+textbook.ID+"/url", http.MethodGet, nil, f.headers); status == http.StatusForbidden {
		t.Fatal("textbook download should pass the gate")
	}
	if status := requestStatus(t, f.server.URL+"/api/v1/resources/"+resource.ID+"/ai?mode=basic", http.MethodGet, nil, f.headers); status != http.StatusOK {
		t.Fatalf("AI access after unlocking = %d", status)
	}
}
