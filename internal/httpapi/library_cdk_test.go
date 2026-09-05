package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/app"
	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

func TestLibraryCDKHTTPFlow(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Addr: "127.0.0.1:0", DBPath: filepath.Join(t.TempDir(), "control.db"), PublicBaseURL: "http://control.test",
		TokenPepper: []byte("token-pepper-token-pepper-token-pepper"), IDPepper: []byte("id-pepper-id-pepper-id-pepper-id-"),
		EncryptionKey: []byte("01234567890123456789012345678901"), AdminKey: "test-password",
		RiskLoginWindow: time.Minute, RiskLoginCount: 5, RiskDeviceCount: 5, EventRetentionDays: 90,
	}
	a, err := app.New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(a, http.NotFoundHandler(), slog.Default()))
	defer ts.Close()

	device := newTestDevice(t, "cdk-http-device")
	registered := register(t, ts.URL, device)
	device.serial, device.token = registered["device_serial"].(string), registered["device_token"].(string)
	challenge := request(t, ts.URL+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{"Authorization": "Device " + device.token})
	student := "2023999001"
	alias := controlcrypto.StudentAlias(student)
	assertedAt := time.Now().UTC().Format(time.RFC3339)
	login := map[string]any{"challenge_id": challenge["challenge_id"], "challenge": challenge["challenge"], "device_serial": device.serial, "student_id": student, "student_alias": alias, "display_name": "CDK 测试", "asserted_at": assertedAt}
	signature := sign(t, device.priv, lines("xzitpocket-control-login", challenge["challenge_id"].(string), challenge["challenge"].(string), device.serial, student, alias, "CDK 测试", assertedAt))
	session := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, login, map[string]string{"Authorization": "Device " + device.token, "X-Device-Signature": signature, "X-Device-Signed-At": assertedAt, "X-Installation-ID": device.installation})
	access := session["access_token"].(string)

	admin := requestWithHeaders(t, ts.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "test-password"}, nil)
	adminHeaders := map[string]string{"Authorization": "Bearer " + admin["access_token"].(string)}
	bank := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks", http.MethodPost, map[string]any{"questionBank": map[string]any{
		"new": true, "requiresCDK": true, "name": "受保护题库", "questions": []any{map[string]any{"questionNumber": 1, "type": "填空题", "title": "第1题", "questionText": "答案", "options": []any{}, "correctAnswer": "答案"}},
	}}, adminHeaders)
	bankID := bank["questionBank"].(map[string]any)["id"].(string)
	if bankID != "QB-001" {
		t.Fatalf("server did not allocate question bank ID: %q", bankID)
	}
	if bank["questionBank"].(map[string]any)["requiresCDK"] != true {
		t.Fatalf("question bank unlock flag missing: %#v", bank)
	}
	userHeaders := map[string]string{"Authorization": "Bearer " + access}
	visible := requestWithHeaders(t, ts.URL+"/api/v1/question-banks", http.MethodGet, nil, userHeaders)
	if visible["total"].(float64) != 1 {
		t.Fatalf("active question bank should be listed even when locked: %#v", visible)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/question-banks/"+bankID, http.MethodGet, nil, userHeaders); status != http.StatusForbidden {
		t.Fatalf("locked question bank status = %d, want %d", status, http.StatusForbidden)
	}
	cdk := requestWithHeaders(t, ts.URL+"/api/v1/admin/library-cdks", http.MethodPost, map[string]any{"count": 3}, adminHeaders)
	createdItems, batchOK := cdk["items"].([]any)
	if !batchOK || len(createdItems) != 3 {
		t.Fatalf("invalid batch CDK create response: %#v", cdk)
	}
	code := createdItems[0].(map[string]any)["code"].(string)
	if len(code) != len("CDK-")+16 || !strings.HasPrefix(code, "CDK-") {
		t.Fatalf("invalid CDK create response: %#v", cdk)
	}
	redeemed := requestWithHeaders(t, ts.URL+"/api/v1/library/cdks/redeem", http.MethodPost, map[string]string{"code": code, "question_bank_id": bankID}, userHeaders)
	if redeemed["redeemed"] != true {
		t.Fatalf("invalid redemption response: %#v", redeemed)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks/"+createdItems[0].(map[string]any)["id"].(string), http.MethodPatch, map[string]any{"status": "disabled"}, adminHeaders); status != http.StatusOK {
		t.Fatalf("used CDK disable status = %d, want %d", status, http.StatusOK)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/library-cdks/"+createdItems[0].(map[string]any)["id"].(string), http.MethodPatch, map[string]any{"status": "active"}, adminHeaders); status != http.StatusOK {
		t.Fatalf("disabled CDK enable status = %d, want %d", status, http.StatusOK)
	}
	requestWithHeaders(t, ts.URL+"/api/v1/question-banks/"+bankID, http.MethodGet, nil, userHeaders)
	requestWithHeaders(t, ts.URL+"/api/v1/library/cdks/redeem", http.MethodPost, map[string]string{"code": strings.ToLower(code), "question_bank_id": bankID}, userHeaders)
	listed := requestWithHeaders(t, ts.URL+"/api/v1/admin/library-cdks", http.MethodGet, nil, adminHeaders)
	items := listed["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("unexpected CDK count: %#v", listed)
	}
	bound := false
	for _, raw := range items {
		if raw.(map[string]any)["bound_student_id"] == student {
			bound = true
			break
		}
	}
	if !bound {
		t.Fatalf("CDK binding was not visible to administrator: %#v", listed)
	}
	codeSearch := requestWithHeaders(t, ts.URL+"/api/v1/admin/library-cdks?q="+code, http.MethodGet, nil, adminHeaders)
	if codeSearch["total"].(float64) != 1 {
		t.Fatalf("CDK code search total = %#v", codeSearch)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/question-banks/"+bankID+"/status", http.MethodPatch, map[string]any{"status": "disabled"}, adminHeaders); status != http.StatusOK {
		t.Fatalf("question bank disable status = %d, want %d", status, http.StatusOK)
	}
	adminBanks := requestWithHeaders(t, ts.URL+"/api/v1/admin/question-banks?status=disabled", http.MethodGet, nil, adminHeaders)
	adminBankItems := adminBanks["items"].([]any)
	if adminBanks["total"].(float64) != 1 || len(adminBankItems) != 1 || adminBankItems[0].(map[string]any)["status"] != "disabled" {
		t.Fatalf("disabled question bank was not retained: %#v", adminBanks)
	}
}
