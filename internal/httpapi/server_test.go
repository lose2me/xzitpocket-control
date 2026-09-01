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
	"net/url"
	"path/filepath"
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
	serviceKey := filepath.Join(dir, "service.key")
	cfg := config.Config{Addr: "127.0.0.1:0", DBPath: filepath.Join(dir, "control.db"), PublicBaseURL: "http://control.test", TokenPepper: []byte("token-pepper-token-pepper-token-pepper"), IDPepper: []byte("id-pepper-id-pepper-id-pepper-id-"), EncryptionKey: []byte("01234567890123456789012345678901"), ServiceSigningKeyPath: serviceKey, AdminBootstrap: "test-password", RiskLoginWindow: time.Minute, RiskLoginCount: 2, RiskDeviceCount: 2, EventRetentionDays: 90}
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
	ch := request(t, ts.URL+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{"Authorization": "Device " + d1.token})
	challenge := ch["challenge"].(string)
	challengeID := ch["challenge_id"].(string)
	student := "2023000001"
	alias := controlcrypto.StudentAlias(student)
	asserted := time.Now().UTC().Format(time.RFC3339)
	payload := map[string]any{"challenge_id": challengeID, "challenge": challenge, "device_serial": d1.serial, "student_id": student, "student_alias": alias, "display_name": "测试用户", "asserted_at": asserted}
	signedAt := time.Now().UTC().Format(time.RFC3339)
	sig := sign(t, d1.priv, lines("xzitpocket-control-login", challengeID, challenge, d1.serial, student, alias, "测试用户", asserted))
	out := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, payload, map[string]string{"Authorization": "Device " + d1.token, "X-Device-Signature": sig, "X-Device-Signed-At": signedAt, "X-Installation-ID": d1.installation})
	access, refresh := out["access_token"].(string), out["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing session: %#v", out)
	}
	_ = request(t, ts.URL+"/api/v1/me", http.MethodGet, nil, map[string]string{"Authorization": "Bearer " + access})
	paidSig := sign(t, d1.priv, lines("xzitpocket-control-service-token", controlcrypto.HashBytesHex([]byte(access)), "document-library", d1.serial, "library:read", signedAt))
	paid := requestWithHeaders(t, ts.URL+"/api/v1/services/document-library/tokens", http.MethodPost, map[string]any{"scope": []string{"library:read"}}, map[string]string{"Authorization": "Bearer " + access, "X-Device-Signature": paidSig, "X-Device-Signed-At": signedAt, "X-Installation-ID": d1.installation})
	if paid["token"] == nil {
		t.Fatalf("missing paid token: %#v", paid)
	}
	_ = requestWithHeaders(t, ts.URL+"/api/v1/telemetry/events", http.MethodPost, []any{map[string]any{"event_id": "e1", "type": "app_start", "occurred_at": time.Now().UTC().Format(time.RFC3339), "properties": map[string]any{"platform": "android"}}}, map[string]string{"Authorization": "Bearer " + access})
	d2 := newTestDevice(t, "inst-two")
	reg2 := register(t, ts.URL, d2)
	d2.serial, d2.token = reg2["device_serial"].(string), reg2["device_token"].(string)
	ch2 := request(t, ts.URL+"/api/v1/auth/challenges", http.MethodPost, nil, map[string]string{"Authorization": "Device " + d2.token})
	a2 := time.Now().UTC().Format(time.RFC3339)
	challengeID2, challenge2 := ch2["challenge_id"].(string), ch2["challenge"].(string)
	sig2 := sign(t, d2.priv, lines("xzitpocket-control-login", challengeID2, challenge2, d2.serial, student, alias, "测试用户", a2))
	out2 := requestWithHeaders(t, ts.URL+"/api/v1/auth/assertions", http.MethodPost, map[string]any{"challenge_id": ch2["challenge_id"], "challenge": ch2["challenge"], "device_serial": d2.serial, "student_id": student, "student_alias": alias, "display_name": "测试用户", "asserted_at": a2}, map[string]string{"Authorization": "Device " + d2.token, "X-Device-Signature": sig2, "X-Device-Signed-At": a2, "X-Installation-ID": d2.installation})
	if out2["access_token"] == nil {
		t.Fatal("second device did not login")
	}
	refreshSig := sign(t, d1.priv, lines("xzitpocket-control-refresh", d1.serial, refresh, signedAt))
	rotated := requestWithHeaders(t, ts.URL+"/api/v1/auth/refresh", http.MethodPost, map[string]string{"refresh_token": refresh}, map[string]string{"X-Device-Signature": refreshSig, "X-Device-Signed-At": signedAt, "X-Installation-ID": d1.installation})
	if rotated["access_token"] == nil {
		t.Fatalf("refresh failed: %#v", rotated)
	}
	client, err := a.UpsertOAuthClient(context.Background(), app.OAuthClientInput{ClientID: "test-client", ClientName: "Test Client", ClientSecret: "client-secret", RedirectURIs: []string{"https://client.example/callback"}, Scopes: []string{"openid", "profile", "student_alias"}})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := a.AuthenticateSession(context.Background(), rotated["access_token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	verifier := "verifier-012345678901234567890123456789012345678901234567890123456789"
	sum := sha256.Sum256([]byte(verifier))
	challengePKCE := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect, err := a.Authorize(context.Background(), principal, app.AuthorizeInput{ClientID: client.ClientID, RedirectURI: "https://client.example/callback", ResponseType: "code", Scope: "openid profile", State: "st", CodeChallenge: challengePKCE, CodeChallengeMethod: "S256"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatal("missing oauth code")
	}
	oauthTokens, err := a.ExchangeOAuthCode(context.Background(), client.ClientID, "client-secret", code, "https://client.example/callback", verifier)
	if err != nil {
		t.Fatal(err)
	}
	oauthToken, err := a.AuthenticateOAuthToken(context.Background(), oauthTokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.OAuthUserinfo(context.Background(), oauthToken)
	if err != nil {
		t.Fatal(err)
	}
	if info["sub"] == nil {
		t.Fatal("missing oauth sub")
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
	der, _ := x509.MarshalPKIXPublicKey(&d.priv.PublicKey)
	pub := base64.RawURLEncoding.EncodeToString(der)
	created := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{"installation_id": d.installation, "public_key": pub, "platform": "android", "app_version": "2.0.0", "created_at": created}
	sig := sign(t, d.priv, lines("xzitpocket-control-device", d.installation, pub, "android", "2.0.0", created))
	return requestWithHeaders(t, base+"/api/v1/devices/register", http.MethodPost, body, map[string]string{"X-Device-Signature": sig})
}
func request(t *testing.T, url, method string, body any, headers map[string]string) map[string]any {
	return requestWithHeaders(t, url, method, body, headers)
}
func requestWithHeaders(t *testing.T, url, method string, body any, headers map[string]string) map[string]any {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, url, bytes.NewReader(data))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder() // server is addressed through URL only in this test helper
	client := http.DefaultClient
	_ = client // replaced by direct transport below
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s -> %d %s", method, url, resp.StatusCode, string(raw))
	}
	_ = res
	return out
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
