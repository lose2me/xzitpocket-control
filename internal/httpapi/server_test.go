package httpapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"xzitpocket-control/internal/app"
	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

type testDevice struct {
	priv                        *ecdsa.PrivateKey
	serial, token, installation string
}

func TestControlFlow(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Addr: "127.0.0.1:0", DBPath: filepath.Join(dir, "control.db"), PublicBaseURL: "http://control.test",
		TokenPepper: []byte("token-pepper-token-pepper-token-pepper"), IDPepper: []byte("id-pepper-id-pepper-id-pepper-id-"),
		EncryptionKey: []byte("01234567890123456789012345678901"),
		AdminKey:      "test-password", RiskLoginWindow: time.Minute, RiskLoginCount: 2, RiskDeviceCount: 2, EventRetentionDays: 90,
	}
	a, err := app.New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := New(a, http.NotFoundHandler(), slog.Default())
	ts := httptest.NewServer(server)
	defer ts.Close()

	d1 := newTestDevice(t, "inst-one")
	reg1 := register(t, ts.URL, d1)
	d1.serial, d1.token = reg1["device_serial"].(string), reg1["device_token"].(string)
	challenge1 := request(t, ts.URL+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{"Authorization": "Device " + d1.token})
	student := "2023000001"
	alias := controlcrypto.StudentAlias(student)
	asserted := time.Now().UTC().Format(time.RFC3339)
	loginPayload := map[string]any{"challenge_id": challenge1["challenge_id"], "challenge": challenge1["challenge"], "device_serial": d1.serial, "student_id": student, "student_alias": alias, "display_name": "测试用户", "asserted_at": asserted}
	signedAt := time.Now().UTC().Format(time.RFC3339)
	loginSignature := sign(t, d1.priv, lines("xzitpocket-control-login", challenge1["challenge_id"].(string), challenge1["challenge"].(string), d1.serial, student, alias, "测试用户", asserted))
	session1 := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, loginPayload, map[string]string{"Authorization": "Device " + d1.token, "X-Device-Signature": loginSignature, "X-Device-Signed-At": signedAt, "X-Installation-ID": d1.installation})
	access, refresh := session1["access_token"].(string), session1["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing session: %#v", session1)
	}
	_ = request(t, ts.URL+"/api/v1/me", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + access})

	questionBank := map[string]any{"questionBank": map[string]any{"new": true, "name": "计算机基础知识测验", "questions": []any{
		map[string]any{"questionNumber": 1, "type": "单选题", "title": "第1题", "questionText": "以下哪个是计算机的核心部件？", "options": []any{map[string]any{"label": "A", "text": "显示器"}, map[string]any{"label": "B", "text": "CPU"}}, "correctAnswer": "B"},
	}}}
	_ = requestWithHeaders(t, ts.URL+"/api/v1/telemetry/events", http.MethodPost, []any{map[string]any{"event_id": "e1", "type": "app_start", "occurred_at": time.Now().UTC().Format(time.RFC3339), "properties": map[string]any{"platform": "android", "app_version": "2.0.0"}}}, map[string]string{"Authorization": "Bearer " + access})

	d2 := newTestDevice(t, "inst-two")
	reg2 := register(t, ts.URL, d2)
	d2.serial, d2.token = reg2["device_serial"].(string), reg2["device_token"].(string)
	challenge2 := request(t, ts.URL+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{"Authorization": "Device " + d2.token})
	asserted2 := time.Now().UTC().Format(time.RFC3339)
	login2 := map[string]any{"challenge_id": challenge2["challenge_id"], "challenge": challenge2["challenge"], "device_serial": d2.serial, "student_id": student, "student_alias": alias, "display_name": "测试用户", "asserted_at": asserted2}
	sig2 := sign(t, d2.priv, lines("xzitpocket-control-login", challenge2["challenge_id"].(string), challenge2["challenge"].(string), d2.serial, student, alias, "测试用户", asserted2))
	secondSession := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, login2, map[string]string{"Authorization": "Device " + d2.token, "X-Device-Signature": sig2, "X-Device-Signed-At": asserted2, "X-Installation-ID": d2.installation})
	if secondSession["access_token"] == nil {
		t.Fatal("second device did not login")
	}

	refreshSignature := sign(t, d1.priv, lines("xzitpocket-control-refresh", d1.serial, refresh, signedAt))
	rotated := requestWithHeaders(t, ts.URL+"/api/v1/auth/refresh", http.MethodPost, map[string]string{"refresh_token": refresh}, map[string]string{"X-Device-Signature": refreshSignature, "X-Device-Signed-At": signedAt, "X-Installation-ID": d1.installation})
	if rotated["access_token"] == nil {
		t.Fatalf("refresh failed: %#v", rotated)
	}
	access = rotated["access_token"].(string)

	admin := requestWithHeaders(t, ts.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "test-password"}, nil)
	adminAccess, ok := admin["access_token"].(string)
	if !ok || adminAccess == "" {
		t.Fatal("admin login failed")
	}
	initialRelease := request(t, ts.URL+"/api/v1/app/release", http.MethodGet, nil, nil)
	if initialRelease["latestVersion"] != "" || initialRelease["downloadUrl"] != "" {
		t.Fatalf("unexpected initial app release config: %#v", initialRelease)
	}
	adminHeaders := map[string]string{"Authorization": "Bearer " + adminAccess}
	errorReport := requestWithHeaders(t, ts.URL+"/api/v1/error-reports", http.MethodPost, map[string]any{
		"event_id": "error-report-test", "occurred_at": time.Now().UTC().Format(time.RFC3339),
		"app_version": "2.0.0", "platform": "android", "title": "登录失败",
		"message": "测试错误", "error": "测试异常", "stack_trace": "测试堆栈",
	}, map[string]string{"Authorization": "Bearer " + access})
	if errorReport["accepted"] != true {
		t.Fatalf("error report was not accepted: %#v", errorReport)
	}
	reports := requestWithHeaders(t, ts.URL+"/api/v1/admin/error-reports?limit=100", http.MethodGet, nil, adminHeaders)
	reportItems, _ := reports["items"].([]any)
	if reports["total"].(float64) != 1 || len(reportItems) != 1 || reportItems[0].(map[string]any)["student_id"] != student {
		t.Fatalf("error report was not grouped by student: %#v", reports)
	}
	reportID := strconv.FormatInt(int64(reportItems[0].(map[string]any)["id"].(float64)), 10)
	ignored := requestWithHeaders(t, ts.URL+"/api/v1/admin/error-reports/"+reportID, http.MethodPatch, map[string]bool{"ignored": true}, adminHeaders)
	if ignored["ignored"] != true {
		t.Fatalf("error report ignore failed: %#v", ignored)
	}
	reports = requestWithHeaders(t, ts.URL+"/api/v1/admin/error-reports?limit=100", http.MethodGet, nil, adminHeaders)
	reportItems, _ = reports["items"].([]any)
	if len(reportItems) != 1 || reportItems[0].(map[string]any)["ignored"] != true {
		t.Fatalf("error report ignored state missing: %#v", reports)
	}
	discardedReport := requestWithHeaders(t, ts.URL+"/api/v1/error-reports", http.MethodPost, map[string]any{
		"event_id": "error-report-ignored", "occurred_at": time.Now().UTC().Format(time.RFC3339),
		"app_version": "2.0.0", "platform": "android", "title": "登录失败", "message": "应被忽略",
	}, map[string]string{"Authorization": "Bearer " + access})
	if discardedReport["accepted"] != false {
		t.Fatalf("ignored error report was accepted: %#v", discardedReport)
	}
	allowed := requestWithHeaders(t, ts.URL+"/api/v1/admin/error-reports/"+reportID, http.MethodPatch, map[string]bool{"ignored": false}, adminHeaders)
	if allowed["ignored"] != false {
		t.Fatalf("error report allow failed: %#v", allowed)
	}
	acceptedReport := requestWithHeaders(t, ts.URL+"/api/v1/error-reports", http.MethodPost, map[string]any{
		"event_id": "error-report-allowed", "occurred_at": time.Now().UTC().Format(time.RFC3339),
		"app_version": "2.0.0", "platform": "android", "title": "登录失败", "message": "应被接收",
	}, map[string]string{"Authorization": "Bearer " + access})
	if acceptedReport["accepted"] != true {
		t.Fatalf("allowed error report was discarded: %#v", acceptedReport)
	}
	clearedReports := requestWithHeaders(t, ts.URL+"/api/v1/admin/error-reports", http.MethodDelete, nil, adminHeaders)
	if clearedReports["deleted"] != float64(2) {
		t.Fatalf("error reports were not cleared: %#v", clearedReports)
	}
	reports = requestWithHeaders(t, ts.URL+"/api/v1/admin/error-reports?limit=100", http.MethodGet, nil, adminHeaders)
	if reports["total"] != float64(0) || len(reports["items"].([]any)) != 0 {
		t.Fatalf("cleared error reports remain visible: %#v", reports)
	}
	reportAfterClear := requestWithHeaders(t, ts.URL+"/api/v1/error-reports", http.MethodPost, map[string]any{
		"event_id": "error-report-after-clear", "occurred_at": time.Now().UTC().Format(time.RFC3339),
		"app_version": "2.0.0", "platform": "android", "title": "登录失败", "message": "清空后应被接收",
	}, map[string]string{"Authorization": "Bearer " + access})
	if reportAfterClear["accepted"] != true {
		t.Fatalf("error report after clear was discarded: %#v", reportAfterClear)
	}
	updatedRelease := requestWithHeaders(t, ts.URL+"/api/v1/admin/app/release", http.MethodPut,
		map[string]string{"latestVersion": "2.0.4", "downloadUrl": "https://download.example.test/xzitpocket.apk"}, adminHeaders)
	if updatedRelease["latestVersion"] != "2.0.4" || updatedRelease["downloadUrl"] != "https://download.example.test/xzitpocket.apk" {
		t.Fatalf("unexpected updated app release config: %#v", updatedRelease)
	}
	publicRelease := request(t, ts.URL+"/api/v1/app/release", http.MethodGet, nil, nil)
	if publicRelease["latestVersion"] != "2.0.4" || publicRelease["downloadUrl"] != "https://download.example.test/xzitpocket.apk" {
		t.Fatalf("public app release config was not updated: %#v", publicRelease)
	}
	auditRows := requestWithHeaders(t, ts.URL+"/api/v1/admin/audit?limit=100", http.MethodGet, nil, adminHeaders)
	auditItems, _ := auditRows["items"].([]any)
	if !containsAuditAction(auditItems, "app_release_update") {
		t.Fatalf("app release update was not audited: %#v", auditRows)
	}
	metrics := requestWithHeaders(t, ts.URL+"/api/v1/admin/metrics/overview", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + adminAccess})
	if metrics["total_users"] == nil {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
	users := requestWithHeaders(t, ts.URL+"/api/v1/admin/users", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + adminAccess})
	items, ok := users["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("unexpected users: %#v", users)
	}
	first, _ := items[0].(map[string]any)
	if first["id"] == nil || first["device_count"] == nil {
		t.Fatalf("user json fields missing: %#v", first)
	}
	detail := requestWithHeaders(t, ts.URL+"/api/v1/admin/users/"+first["id"].(string), http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + adminAccess})
	if _, leaked := detail["access_ciphertext"]; leaked {
		t.Fatal("secret ciphertext leaked in user detail")
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/users/"+first["id"].(string)+"/status", http.MethodPatch, map[string]string{"status": "disabled"}, map[string]string{"Authorization": "Bearer " + adminAccess}); status != http.StatusOK {
		t.Fatalf("disable user status = %d, want %d", status, http.StatusOK)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/me", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + access}); status != http.StatusOK {
		t.Fatalf("disabled user /me status = %d, want %d", status, http.StatusOK)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/telemetry/events", http.MethodPost, []map[string]any{{"event_id": "disabled-user-event", "type": "foreground", "occurred_at": time.Now().UTC().Format(time.RFC3339), "properties": map[string]any{}}}, map[string]string{"Authorization": "Bearer " + access}); status != http.StatusAccepted {
		t.Fatalf("disabled user telemetry status = %d, want %d", status, http.StatusAccepted)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/question-banks", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + access}); status != http.StatusForbidden {
		t.Fatalf("disabled user question-bank status = %d, want %d", status, http.StatusForbidden)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/users/"+first["id"].(string)+"/status", http.MethodPatch, map[string]string{"status": "active"}, map[string]string{"Authorization": "Bearer " + adminAccess}); status != http.StatusOK {
		t.Fatalf("enable user status = %d, want %d", status, http.StatusOK)
	}

	for _, path := range []string{
		"/api/v1/admin/metrics/series?days=7",
		"/api/v1/admin/metrics/breakdown",
		"/api/v1/admin/devices?limit=25",
		"/api/v1/admin/risk-events?limit=25",
		"/api/v1/admin/question-banks",
		"/api/v1/admin/audit?limit=25",
	} {
		_ = requestWithHeaders(t, ts.URL+path, http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + adminAccess})
	}
	if status := requestStatus(t, ts.URL+"/api/v1", http.MethodGet, nil, nil); status != http.StatusNotFound {
		t.Fatalf("API base without a resource should return JSON 404, got %d", status)
	}
	createdBank := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks", http.MethodPost, questionBank, map[string]string{"Authorization": "Bearer " + adminAccess})
	if createdBank["questionBank"] == nil {
		t.Fatalf("question bank was not created: %#v", createdBank)
	}
	adminPreview := requestWithHeaders(t, ts.URL+"/api/v1/question-banks/QB-001", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + adminAccess})
	if adminPreview["questionBank"] == nil {
		t.Fatalf("admin question bank preview failed: %#v", adminPreview)
	}
	publicBank := requestWithHeaders(t, ts.URL+"/api/v1/question-banks/QB-001", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + access})
	if publicBank["questionBank"] == nil {
		t.Fatalf("question bank read failed: %#v", publicBank)
	}
	malformedPaths := []struct {
		path    string
		method  string
		headers map[string]string
	}{
		{path: "/api/v1/unknown", method: http.MethodGet, headers: nil},
		{path: "/api/v1/admin/users/" + first["id"].(string) + "/extra", method: http.MethodGet, headers: map[string]string{"Authorization": "Bearer " + adminAccess}},
		{path: "/api/v1/admin/risk-events/risk_test/extra", method: http.MethodPatch, headers: map[string]string{"Authorization": "Bearer " + adminAccess}},
		{path: "/api/v1/admin/question-banks/QB-001/extra", method: http.MethodPut, headers: map[string]string{"Authorization": "Bearer " + adminAccess}},
	}
	for _, item := range malformedPaths {
		if status := requestStatus(t, ts.URL+item.path, item.method, nil, item.headers); status != http.StatusNotFound {
			t.Fatalf("malformed API path %s returned %d", item.path, status)
		}
	}

}

func newTestDevice(t *testing.T, installation string) *testDevice {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testDevice{priv: priv, installation: installation}
}

func register(t *testing.T, base string, d *testDevice) map[string]any {
	der, err := x509.MarshalPKIXPublicKey(&d.priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pub := base64.RawURLEncoding.EncodeToString(der)
	created := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{"installation_id": d.installation, "public_key": pub, "platform": "android", "app_version": "2.0.0", "created_at": created}
	sig := sign(t, d.priv, lines("xzitpocket-control-device", d.installation, pub, "android", "2.0.0", created))
	return requestWithHeaders(t, base+"/api/v1/devices/register", http.MethodPost, body, map[string]string{"X-Device-Signature": sig})
}

func request(t *testing.T, endpoint, method string, body any, headers map[string]string) map[string]any {
	return requestWithHeaders(t, endpoint, method, body, headers)
}

func requestWithHeaders(t *testing.T, endpoint, method string, body any, headers map[string]string) map[string]any {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s -> %d %s", method, endpoint, resp.StatusCode, string(raw))
	}
	return out
}

func requestStatus(t *testing.T, endpoint, method string, body any, headers map[string]string) int {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func containsAuditAction(items []any, action string) bool {
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if ok && item["action"] == action {
			return true
		}
	}
	return false
}

func lines(values ...string) string {
	b, _ := controlcrypto.CanonicalLines(values...)
	return string(b)
}

func sign(t *testing.T, priv *ecdsa.PrivateKey, message string) string {
	sum := sha256.Sum256([]byte(message))
	raw, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
