package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEnv(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const validSecrets = "CONTROL_TOKEN_PEPPER=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE\n" +
	"CONTROL_ID_PEPPER=YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmI\n" +
	"CONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n"

func TestLoadCreatesDefaultConfigWhenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" || cfg.DBPath != "data/control.db" || cfg.AdminKey != "change-me" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if len(cfg.TokenPepper) < 32 || len(cfg.IDPepper) < 32 || len(cfg.EncryptionKey) != 32 {
		t.Fatalf("generated secrets = %d/%d/%d", len(cfg.TokenPepper), len(cfg.IDPepper), len(cfg.EncryptionKey))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "data", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "CONTROL_ADDR=127.0.0.1:8080") {
		t.Fatalf("written env = %q", raw)
	}
	info, err := os.Stat(filepath.Join(dir, "data", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env mode = %o", info.Mode().Perm())
	}
}

func TestLoadReadsOverridesAndClampsInvalidValues(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeEnv(t, dir, "# comment\n"+
		"export CONTROL_ADDR = 0.0.0.0:7001\n"+
		"CONTROL_DB=state/custom.db\n"+
		"CONTROL_PUBLIC_BASE_URL=https://con.example.test/\n"+
		"CONTROL_ADMIN_KEY='admin key'\n"+
		"The=quoted \"value # kept\"\n"+
		"QUOTED=\"line\\nbreak\"\n"+
		"TRAILING=value # comment\n"+
		"CONTROL_EVENT_RETENTION_DAYS=0\n"+
		"CONTROL_AUDIT_RETENTION_DAYS=abc\n"+
		"CONTROL_OVERVIEW_SCALE_PERCENT=999999\n"+
		"CONTROL_RISK_LOGIN_WINDOW=bogus\n"+
		"CONTROL_RISK_LOGIN_COUNT=0\n"+
		"CONTROL_RISK_DEVICE_COUNT=-1\n"+
		"CONTROL_AI_API_KEY= secret \n"+
		"CONTROL_AI_MAX_ATTEMPTS=99\n"+
		"CONTROL_AI_IMAGE_QUALITY=5\n"+
		"CONTROL_AI_IMAGE_MAX_EDGE=100\n"+
		"CONTROL_ACCEPT_COURSES=false\n"+
		"CONTROL_ACCEPT_TEXTBOOKS=not-a-bool\n"+
		validSecrets)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:7001" || cfg.DBPath != "state/custom.db" {
		t.Fatalf("overrides = %+v", cfg)
	}
	if cfg.PublicBaseURL != "https://con.example.test" || cfg.AdminKey != "admin key" {
		t.Fatalf("trimmed values = %q / %q", cfg.PublicBaseURL, cfg.AdminKey)
	}
	if cfg.EventRetentionDays != 90 || cfg.AuditRetentionDays != 30 || cfg.OverviewScalePercent != 100 {
		t.Fatalf("clamped retention = %d/%d/%d", cfg.EventRetentionDays, cfg.AuditRetentionDays, cfg.OverviewScalePercent)
	}
	if cfg.RiskLoginWindow != 10*time.Minute || cfg.RiskLoginCount != 5 || cfg.RiskDeviceCount != 5 {
		t.Fatalf("clamped risk = %v/%d/%d", cfg.RiskLoginWindow, cfg.RiskLoginCount, cfg.RiskDeviceCount)
	}
	if cfg.AIAPIKey != "secret" || !cfg.AIAnalysisEnabled {
		t.Fatalf("ai key = %q enabled=%v", cfg.AIAPIKey, cfg.AIAnalysisEnabled)
	}
	if cfg.AIMaxAttempts != 3 || cfg.AIImageQuality != 80 || cfg.AIImageMaxEdge != 1600 {
		t.Fatalf("clamped ai = %d/%d/%d", cfg.AIMaxAttempts, cfg.AIImageQuality, cfg.AIImageMaxEdge)
	}
	if cfg.IngestCourses.Enabled() {
		t.Fatal("courses ingest should be disabled")
	}
	if !cfg.IngestTextbooks.Enabled() {
		t.Fatal("invalid switch should keep accepting")
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); err != nil {
		t.Fatalf("data dir not created: %v", err)
	}
}

func TestLoadFillsMissingSecretsInExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeEnv(t, dir, "CONTROL_ADDR=127.0.0.1:1\nCONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TokenPepper) < 32 || len(cfg.IDPepper) < 32 {
		t.Fatalf("missing secrets were not generated: %+v", cfg)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "data", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "CONTROL_TOKEN_PEPPER=") || !strings.Contains(string(raw), "CONTROL_ID_PEPPER=") {
		t.Fatalf("secrets were not appended: %q", raw)
	}

	// A second load must keep the generated values.
	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(again.TokenPepper) != string(cfg.TokenPepper) {
		t.Fatal("token pepper changed between loads")
	}
}

func TestLoadRejectsBrokenConfigFiles(t *testing.T) {
	cases := map[string]string{
		"invalid line":  "CONTROL_ADDR\n",
		"invalid key":   "BAD-KEY=1\n",
		"missing token": "CONTROL_ID_PEPPER=id-pepper\nCONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n",
		"short pepper":  "CONTROL_TOKEN_PEPPER=c2hvcnQ\nCONTROL_ID_PEPPER=id-pepper\nCONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n",
		"bad encoding":  "CONTROL_TOKEN_PEPPER=!!!\nCONTROL_ID_PEPPER=id-pepper\nCONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n",
		"bad key size":  "CONTROL_TOKEN_PEPPER=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE\nCONTROL_ID_PEPPER=id-pepper\nCONTROL_ENCRYPTION_KEY=c2hvcnQ\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			writeEnv(t, dir, body)
			if _, err := Load(); err == nil {
				t.Fatalf("expected %s to fail", name)
			}
		})
	}
}

func TestParseDotEnvValueVariants(t *testing.T) {
	cases := map[string]string{
		`"quoted"`:      "quoted",
		`"escaped\n"`:   "escaped\n",
		`'single'`:      "single",
		`plain # note`:  "plain",
		`  spaced  `:    "spaced",
		`"unterminated`: "\"unterminated",
	}
	for input, want := range cases {
		if got := parseDotEnvValue(input); got != want {
			t.Fatalf("parseDotEnvValue(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadFailsWhenConfigDirectoryCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// A file where the data directory should be makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(dir, "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected directory creation failure")
	}
}

func TestConfigValueAndParsers(t *testing.T) {
	values := map[string]string{"A": " value ", "B": "", "OVERRIDE": "5"}
	if got := configValue(values, "A", "fallback"); got != "value" {
		t.Fatalf("configValue = %q", got)
	}
	if got := configValue(values, "B", "fallback"); got != "fallback" {
		t.Fatalf("configValue empty = %q", got)
	}
	if got := intConfigValue(values, "MISSING", 7); got != 7 {
		t.Fatalf("intConfigValue fallback = %d", got)
	}
	if got := intConfigValue(map[string]string{"N": "12"}, "N", 7); got != 12 {
		t.Fatalf("intConfigValue = %d", got)
	}
	if got := durationConfigValue(map[string]string{"D": "2h"}, "D", time.Minute); got != 2*time.Hour {
		t.Fatalf("durationConfigValue = %v", got)
	}
	if got := durationConfigValue(map[string]string{"D": "-2h"}, "D", time.Minute); got != time.Minute {
		t.Fatalf("durationConfigValue negative = %v", got)
	}
	if got := durationConfigValue(map[string]string{"D": ""}, "D", time.Minute); got != time.Minute {
		t.Fatalf("durationConfigValue empty = %v", got)
	}
	if _, err := secret(map[string]string{}, "K"); err == nil {
		t.Fatal("missing secret should fail")
	}
	if _, err := encryptionKey(map[string]string{"K": "!!!"}, "K"); err == nil {
		t.Fatal("invalid base64 key should fail")
	}
	if _, err := encryptionKey(map[string]string{}, "K"); err == nil {
		t.Fatal("missing encryption key should fail")
	}
}

func TestParseAdminPermsErrors(t *testing.T) {
	if perms, err := ParseAdminPerms(" UserS = RW , * = rw "); err != nil || perms["users"] != "rw" || perms["*"] != "rw" {
		t.Fatalf("ParseAdminPerms = %v, %v", perms, err)
	}
	if _, err := ParseAdminPerms(" * ,, "); err == nil {
		t.Fatal("a bare wildcard needs an explicit access level")
	}
	if perms, err := ParseAdminPerms("users=rw,,risk=r"); err != nil || len(perms) != 2 {
		t.Fatalf("ParseAdminPerms empty part = %v, %v", perms, err)
	}
	for _, raw := range []string{"users", "users=x", "unknown-page=r", "*=r"} {
		if _, err := ParseAdminPerms(raw); err == nil {
			t.Fatalf("ParseAdminPerms(%q) should fail", raw)
		}
	}
}
