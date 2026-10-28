package httpapi

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
)

const testStudent = "2023000001"

func registerDevice(t *testing.T, base string, d *testDevice) {
	t.Helper()
	out := register(t, base, d)
	d.serial, d.token = out["device_serial"].(string), out["device_token"].(string)
	if d.serial == "" || d.token == "" {
		t.Fatalf("register = %#v", out)
	}
}

func issueChallenge(t *testing.T, base string, d *testDevice, student string) map[string]any {
	t.Helper()
	return request(t, base+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{
		"X-Student-ID": student, "Authorization": "Device " + d.token,
	})
}

func loginMessage(challenge map[string]any, d *testDevice, student, alias, display, college, class, major, asserted string) string {
	return lines("xzitpocket-control-login", challenge["challenge_id"].(string), challenge["challenge"].(string), d.serial, student, alias, display, college, class, major, asserted)
}

func loginBody(challenge map[string]any, d *testDevice, student, alias, display, college, class, major, asserted string) map[string]any {
	return map[string]any{
		"challenge_id": challenge["challenge_id"], "challenge": challenge["challenge"], "device_serial": d.serial,
		"student_id": student, "student_alias": alias, "display_name": display,
		"college_name": college, "class_name": class, "major_name": major, "asserted_at": asserted,
	}
}

func loginHeaders(d *testDevice, student, signature, signedAt string) map[string]string {
	return map[string]string{
		"X-Student-ID": student, "Authorization": "Device " + d.token,
		"X-Device-Signature": signature, "X-Device-Signed-At": signedAt, "X-Installation-ID": d.installation,
	}
}

func mustJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestDeviceRegistrationFailures(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	post := func(body map[string]any, headers map[string]string) int {
		merged := map[string]string{"X-Student-ID": testStudent}
		for key, value := range headers {
			merged[key] = value
		}
		return requestStatus(t, ts.URL+"/api/v1/devices/register", http.MethodPost, body, merged)
	}
	if status := post(map[string]any{}, nil); status != http.StatusBadRequest {
		t.Fatalf("empty registration = %d", status)
	}
	if status := post(map[string]any{"installation_id": "i", "public_key": "k", "platform": "", "app_version": ""}, nil); status != http.StatusBadRequest {
		t.Fatalf("missing platform = %d", status)
	}
	device := newTestDevice(t, "fail-timestamp")
	der, _ := x509.MarshalPKIXPublicKey(&device.priv.PublicKey)
	pub := base64.RawURLEncoding.EncodeToString(der)
	stale := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	body := map[string]any{"installation_id": device.installation, "public_key": pub, "platform": "android", "app_version": "2.0.0", "created_at": stale}
	sig := sign(t, device.priv, lines("xzitpocket-control-device", device.installation, pub, "android", "2.0.0", stale))
	if status := post(body, map[string]string{"X-Device-Signature": sig}); status != http.StatusBadRequest {
		t.Fatalf("stale created_at = %d", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	body["created_at"] = now
	body["public_key"] = "!!!"
	if status := post(body, nil); status != http.StatusBadRequest {
		t.Fatalf("invalid public key = %d", status)
	}
	body["public_key"] = pub
	status, _, raw := rawRequest(t, ts.URL+"/api/v1/devices/register", http.MethodPost, mustJSON(body), map[string]string{"X-Student-ID": testStudent, "X-Device-Signature": sign(t, device.priv, "wrong")})
	if status != http.StatusUnauthorized {
		t.Fatalf("invalid signature = %d %s", status, raw)
	}

	registerDevice(t, ts.URL, device)
	if status := post(body, map[string]string{"X-Device-Signature": sign(t, device.priv, lines("xzitpocket-control-device", device.installation, pub, "android", "2.0.0", now))}); status != http.StatusConflict {
		t.Fatalf("re-registration = %d", status)
	}

	other := newTestDevice(t, device.installation)
	otherDer, _ := x509.MarshalPKIXPublicKey(&other.priv.PublicKey)
	otherPub := base64.RawURLEncoding.EncodeToString(otherDer)
	otherBody := map[string]any{"installation_id": device.installation, "public_key": otherPub, "platform": "android", "app_version": "2.0.0", "created_at": now}
	otherSig := sign(t, other.priv, lines("xzitpocket-control-device", device.installation, otherPub, "android", "2.0.0", now))
	if status := post(otherBody, map[string]string{"X-Device-Signature": otherSig}); status != http.StatusConflict {
		t.Fatalf("installation takeover = %d", status)
	}
}

func TestAssertionValidationFailures(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	device := newTestDevice(t, "assert-fail")
	registerDevice(t, ts.URL, device)
	asserted := time.Now().UTC().Format(time.RFC3339)
	alias := controlcrypto.StudentAlias(testStudent)

	assert := func(payload map[string]any, headers map[string]string) int {
		return requestStatus(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, payload, headers)
	}
	signed := func(mutate func(*map[string]any), headers func(map[string]string) map[string]string) int {
		challenge2 := issueChallenge(t, ts.URL, device, testStudent)
		body := loginBody(challenge2, device, testStudent, alias, "测试用户", "计算机学院", "计科2301", "计算机", asserted)
		if mutate != nil {
			mutate(&body)
		}
		sig := sign(t, device.priv, loginMessage(challenge2, device, body["student_id"].(string), body["student_alias"].(string), body["display_name"].(string), body["college_name"].(string), body["class_name"].(string), body["major_name"].(string), body["asserted_at"].(string)))
		requestHeaders := loginHeaders(device, testStudent, sig, asserted)
		if headers != nil {
			requestHeaders = headers(requestHeaders)
		}
		return assert(body, requestHeaders)
	}

	cases := []struct {
		name    string
		mutate  func(*map[string]any)
		headers func(map[string]string) map[string]string
		want    int
	}{
		{"missing installation", nil, func(h map[string]string) map[string]string { delete(h, "X-Installation-ID"); return h }, http.StatusUnauthorized},
		{"wrong installation", nil, func(h map[string]string) map[string]string { h["X-Installation-ID"] = "other"; return h }, http.StatusUnauthorized},
		{"wrong device serial", func(b *map[string]any) { (*b)["device_serial"] = "dev_other" }, nil, http.StatusUnauthorized},
		{"invalid student id", func(b *map[string]any) {
			(*b)["student_id"] = "学号"
			(*b)["student_alias"] = controlcrypto.StudentAlias("学号")
		}, func(h map[string]string) map[string]string { h["X-Student-ID"] = "学号"; return h }, http.StatusBadRequest},
		{"invalid display name", func(b *map[string]any) { (*b)["display_name"] = strings.Repeat("名", 200) }, nil, http.StatusBadRequest},
		{"invalid class name", func(b *map[string]any) { (*b)["class_name"] = "班级\n注入" }, nil, http.StatusBadRequest},
		{"stale asserted_at", func(b *map[string]any) { (*b)["asserted_at"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339) }, nil, http.StatusBadRequest},
		{"missing signed at", nil, func(h map[string]string) map[string]string { delete(h, "X-Device-Signed-At"); return h }, http.StatusUnauthorized},
		{"stale signed at", nil, func(h map[string]string) map[string]string {
			h["X-Device-Signed-At"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
			return h
		}, http.StatusBadRequest},
		{"bad signature", nil, func(h map[string]string) map[string]string { h["X-Device-Signature"] = "!!!"; return h }, http.StatusUnauthorized},
		{"alias mismatch", func(b *map[string]any) { (*b)["student_alias"] = "wrong-alias" }, nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := signed(tc.mutate, tc.headers); got != tc.want {
				t.Fatalf("%s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}

	// A malformed body is rejected before the signature is checked.
	if status := assert(nil, map[string]string{"Authorization": "Device " + device.token, "X-Student-ID": testStudent}); status != http.StatusBadRequest {
		t.Fatalf("malformed assertion = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, map[string]any{"student_id": testStudent}, map[string]string{"X-Student-ID": "2023000002"}); status != http.StatusUnauthorized {
		t.Fatalf("unknown device token = %d", status)
	}
}

func TestSuccessfulLoginReplayAndRefresh(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	device := newTestDevice(t, "refresh-flow")
	registerDevice(t, ts.URL, device)
	asserted := time.Now().UTC().Format(time.RFC3339)
	alias := controlcrypto.StudentAlias(testStudent)
	challenge := issueChallenge(t, ts.URL, device, testStudent)
	body := loginBody(challenge, device, testStudent, alias, "测试用户", "计算机学院", "计科2301", "计算机", asserted)
	sig := sign(t, device.priv, loginMessage(challenge, device, testStudent, alias, "测试用户", "计算机学院", "计科2301", "计算机", asserted))
	session := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, body, loginHeaders(device, testStudent, sig, asserted))
	refreshToken := session["refresh_token"].(string)
	access := session["access_token"].(string)

	// The same challenge cannot be consumed twice.
	replay := requestStatus(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, body, loginHeaders(device, testStudent, sig, asserted))
	if replay != http.StatusUnauthorized {
		t.Fatalf("challenge replay = %d", replay)
	}

	refresh := func(token string, headers map[string]string) int {
		return requestStatus(t, ts.URL+"/api/v1/auth/refresh", http.MethodPost, map[string]any{"refresh_token": token}, headers)
	}
	signedAt := time.Now().UTC().Format(time.RFC3339)
	refreshSig := sign(t, device.priv, lines("xzitpocket-control-refresh", device.serial, refreshToken, signedAt))
	refreshHeaders := map[string]string{
		"X-Student-ID": testStudent, "X-Installation-ID": device.installation,
		"X-Device-Signed-At": signedAt, "X-Device-Signature": refreshSig,
	}
	if status := refresh("", map[string]string{"X-Student-ID": testStudent}); status != http.StatusUnauthorized {
		t.Fatalf("empty refresh token = %d", status)
	}
	if status := refresh("unknown-token", refreshHeaders); status != http.StatusUnauthorized {
		t.Fatalf("unknown refresh token = %d", status)
	}
	if status := refresh(refreshToken, map[string]string{"X-Student-ID": testStudent}); status != http.StatusUnauthorized {
		t.Fatalf("refresh without installation = %d", status)
	}
	if status := refresh(refreshToken, map[string]string{"X-Student-ID": testStudent, "X-Installation-ID": "other", "X-Device-Signed-At": signedAt}); status != http.StatusUnauthorized {
		t.Fatalf("refresh installation mismatch = %d", status)
	}
	if status := refresh(refreshToken, map[string]string{"X-Student-ID": testStudent, "X-Installation-ID": device.installation}); status != http.StatusUnauthorized {
		t.Fatalf("refresh without signed at = %d", status)
	}
	if status := refresh(refreshToken, map[string]string{"X-Student-ID": testStudent, "X-Installation-ID": device.installation, "X-Device-Signed-At": "bogus"}); status != http.StatusBadRequest {
		t.Fatalf("refresh invalid signed at = %d", status)
	}
	if status := refresh(refreshToken, map[string]string{"X-Student-ID": testStudent, "X-Installation-ID": device.installation, "X-Device-Signed-At": signedAt, "X-Device-Signature": "!!!"}); status != http.StatusUnauthorized {
		t.Fatalf("refresh bad signature = %d", status)
	}
	if status := refresh(refreshToken, map[string]string{"X-Student-ID": "2023000002", "X-Installation-ID": device.installation, "X-Device-Signed-At": signedAt, "X-Device-Signature": refreshSig}); status != http.StatusForbidden {
		t.Fatalf("refresh other student = %d", status)
	}
	rotated := requestWithHeaders(t, ts.URL+"/api/v1/auth/refresh", http.MethodPost, map[string]any{"refresh_token": refreshToken}, refreshHeaders)
	if rotated["refresh_token"] == refreshToken || rotated["access_token"] == access {
		t.Fatalf("refresh did not rotate tokens: %#v", rotated)
	}
	// Rotating invalidates the previous refresh token.
	if status := refresh(refreshToken, refreshHeaders); status != http.StatusUnauthorized {
		t.Fatalf("refresh replay = %d", status)
	}

	if status := requestStatus(t, ts.URL+"/api/v1/auth/revoke", http.MethodPost, nil, map[string]string{"Authorization": "Bearer " + rotated["access_token"].(string), "X-Student-ID": testStudent}); status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/me", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + rotated["access_token"].(string)}); status != http.StatusUnauthorized {
		t.Fatalf("me after revoke = %d", status)
	}
}

func TestDisabledAccountBlocksLoginAndRefresh(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	ctx := request(t, ts.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "test-password"}, nil)
	adminHeaders := map[string]string{"Authorization": "Bearer " + ctx["access_token"].(string)}

	device := newTestDevice(t, "disabled-flow")
	registerDevice(t, ts.URL, device)
	asserted := time.Now().UTC().Format(time.RFC3339)
	alias := controlcrypto.StudentAlias(testStudent)
	challenge := issueChallenge(t, ts.URL, device, testStudent)
	body := loginBody(challenge, device, testStudent, alias, "测试用户", "", "", "", asserted)
	sig := sign(t, device.priv, loginMessage(challenge, device, testStudent, alias, "测试用户", "", "", "", asserted))
	session := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, body, loginHeaders(device, testStudent, sig, asserted))
	userID := session["user"].(map[string]any)["id"].(string)
	if status := requestStatus(t, ts.URL+"/api/v1/admin/users/"+userID+"/status", http.MethodPatch, map[string]any{"status": "disabled"}, adminHeaders); status != http.StatusOK {
		t.Fatalf("disable user = %d", status)
	}

	challenge2 := issueChallenge(t, ts.URL, device, testStudent)
	alias2 := controlcrypto.StudentAlias(testStudent)
	asserted2 := time.Now().UTC().Format(time.RFC3339)
	body2 := loginBody(challenge2, device, testStudent, alias2, "测试用户", "", "", "", asserted2)
	sig2 := sign(t, device.priv, loginMessage(challenge2, device, testStudent, alias2, "测试用户", "", "", "", asserted2))
	if status := requestStatus(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, body2, loginHeaders(device, testStudent, sig2, asserted2)); status != http.StatusForbidden {
		t.Fatalf("disabled login = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/auth/login-eligibility", http.MethodPost, map[string]string{"student_id": testStudent}, map[string]string{"Authorization": "Device " + device.token, "X-Student-ID": testStudent}); status != http.StatusForbidden {
		t.Fatalf("eligibility for disabled user = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/auth/login-eligibility", http.MethodPost, map[string]string{"student_id": "学号"}, map[string]string{"Authorization": "Device " + device.token, "X-Student-ID": "学号"}); status != http.StatusBadRequest {
		t.Fatalf("eligibility invalid student = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/auth/login-eligibility", http.MethodPost, map[string]string{"student_id": "2023999999"}, map[string]string{"Authorization": "Device " + device.token, "X-Student-ID": "2023999999"}); status != http.StatusOK {
		t.Fatalf("eligibility for unknown student = %d", status)
	}

	refreshToken := session["refresh_token"].(string)
	signedAt := time.Now().UTC().Format(time.RFC3339)
	refreshSig := sign(t, device.priv, lines("xzitpocket-control-refresh", device.serial, refreshToken, signedAt))
	refreshHeaders := map[string]string{
		"X-Student-ID": testStudent, "X-Installation-ID": device.installation,
		"X-Device-Signed-At": signedAt, "X-Device-Signature": refreshSig,
	}
	// Disabling an account also revokes its sessions, so the refresh token may
	// already be invalid: both rejections are correct.
	if status := requestStatus(t, ts.URL+"/api/v1/auth/refresh", http.MethodPost, map[string]any{"refresh_token": refreshToken}, refreshHeaders); status != http.StatusForbidden && status != http.StatusUnauthorized {
		t.Fatalf("refresh for disabled user = %d", status)
	}
}

func TestRevokedDeviceCannotRefresh(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	device := newTestDevice(t, "revoked-flow")
	registerDevice(t, ts.URL, device)
	asserted := time.Now().UTC().Format(time.RFC3339)
	alias := controlcrypto.StudentAlias(testStudent)
	challenge := issueChallenge(t, ts.URL, device, testStudent)
	body := loginBody(challenge, device, testStudent, alias, "测试用户", "", "", "", asserted)
	sig := sign(t, device.priv, loginMessage(challenge, device, testStudent, alias, "测试用户", "", "", "", asserted))
	session := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, body, loginHeaders(device, testStudent, sig, asserted))
	deviceID := session["device"].(map[string]any)["id"].(string)

	admin := request(t, ts.URL+"/api/v1/admin/session", http.MethodPost, map[string]string{"key": "test-password"}, nil)
	adminHeaders := map[string]string{"Authorization": "Bearer " + admin["access_token"].(string)}
	if status := requestStatus(t, ts.URL+"/api/v1/admin/devices/"+deviceID+"/status", http.MethodPatch, map[string]any{"status": "revoked"}, adminHeaders); status != http.StatusOK {
		t.Fatalf("revoke device = %d", status)
	}

	refreshToken := session["refresh_token"].(string)
	signedAt := time.Now().UTC().Format(time.RFC3339)
	refreshSig := sign(t, device.priv, lines("xzitpocket-control-refresh", device.serial, refreshToken, signedAt))
	if status := requestStatus(t, ts.URL+"/api/v1/auth/refresh", http.MethodPost, map[string]any{"refresh_token": refreshToken}, map[string]string{
		"X-Student-ID": testStudent, "X-Installation-ID": device.installation,
		"X-Device-Signed-At": signedAt, "X-Device-Signature": refreshSig,
	}); status != http.StatusUnauthorized {
		t.Fatalf("refresh for revoked device = %d", status)
	}
	if status := requestStatus(t, ts.URL+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{"X-Student-ID": testStudent, "Authorization": "Device " + device.token}); status != http.StatusUnauthorized {
		t.Fatalf("challenge for revoked device = %d", status)
	}
}

func TestRefreshRejectsInvalidDeviceKeys(t *testing.T) {
	ts, _, _ := newAdminServer(t)
	device := newTestDevice(t, "bad-key-flow")
	registerDevice(t, ts.URL, device)
	asserted := time.Now().UTC().Format(time.RFC3339)
	alias := controlcrypto.StudentAlias(testStudent)
	challenge := issueChallenge(t, ts.URL, device, testStudent)
	body := loginBody(challenge, device, testStudent, alias, "测试用户", "", "", "", asserted)
	sig := sign(t, device.priv, loginMessage(challenge, device, testStudent, alias, "测试用户", "", "", "", asserted))
	session := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, body, loginHeaders(device, testStudent, sig, asserted))

	// Corrupt the stored public key: parsing fails and refresh is refused.
	if _, err := ts.Config.Handler.(*Server).App.Store.DB.Exec("UPDATE devices SET public_key = '!!!' WHERE installation_id = ?", device.installation); err != nil {
		t.Fatal(err)
	}
	refreshToken := session["refresh_token"].(string)
	signedAt := time.Now().UTC().Format(time.RFC3339)
	refreshSig := sign(t, device.priv, lines("xzitpocket-control-refresh", device.serial, refreshToken, signedAt))
	status := requestStatus(t, ts.URL+"/api/v1/auth/refresh", http.MethodPost, map[string]any{"refresh_token": refreshToken}, map[string]string{
		"X-Student-ID": testStudent, "X-Installation-ID": device.installation,
		"X-Device-Signed-At": signedAt, "X-Device-Signature": refreshSig,
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("refresh with corrupt key = %d", status)
	}
}
