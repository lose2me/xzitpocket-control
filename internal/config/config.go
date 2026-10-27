package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"xzitpocket-control/internal/ai"
)

type Config struct {
	Path                 string
	Addr                 string
	DBPath               string
	PublicBaseURL        string
	WebDir               string
	TokenPepper          []byte
	IDPepper             []byte
	EncryptionKey        []byte
	AdminKey             string
	EventRetentionDays   int
	AuditRetentionDays   int
	OverviewScalePercent int
	RiskLoginWindow      time.Duration
	RiskLoginCount       int
	RiskDeviceCount      int

	// Object storage + CDN used for textbook PDFs and their covers.
	COSRegion    string
	COSBucket    string
	COSSecretID  string
	COSSecretKey string
	// COSWebsiteBucket is the website/APK bucket shown next to the assets bucket.
	COSWebsiteBucket string
	CDNDomain        string
	CDNAuthKey       string
	CDNURLTTL        time.Duration

	// Tencent Cloud credentials for this host's Lighthouse monitoring.
	MonitorSecretID  string
	MonitorSecretKey string

	// AIAnalysis configures the DeepSeek-compatible vision model used to
	// analyse exam PDFs. The API key is never logged.
	// Ingest switches. Every report a client sends can be turned off from the
	// console without shipping a new app build. The zero value accepts, so a
	// config built by hand keeps ingesting.
	IngestCourses     IngestSwitch
	IngestTextbooks   IngestSwitch
	IngestSubmissions IngestSwitch
	IngestUserInfo    IngestSwitch
	IngestEvents      IngestSwitch

	AIAnalysisEnabled bool
	AIAPIKey          string
	AIBaseURL         string
	AIModel           string
	AIWindows         string
	AITimezone        string
	AIRequestTimeout  time.Duration
	AIMaxAttempts     int
	AIImageQuality    int
	AIImageMaxEdge    int
	AIWorkDir         string
	AIMathJaxDir      string

	// Prompts are editable runtime settings; empty values fall back to the
	// built-in templates in the ai package.
	AIPrecheckSystem      string
	AIAnalysisSystem      string
	AIPrecheckInstruction string
	AIAnalysisInstruction string
}

// AdminPages are the console pages that can be granted read/write access.
var AdminPages = []string{
	"overview", "users", "devices", "risk", "error-reports", "library",
	"textbooks", "resources", "courses", "share-codes", "config", "audit",
}

func Load() (Config, error) {
	configPath := filepath.Join("data", ".env")
	values, err := loadDotEnv(configPath)
	if err != nil {
		return Config{}, err
	}
	if err := ensureSecretValues(configPath, values); err != nil {
		return Config{}, err
	}
	dbPath := configValue(values, "CONTROL_DB", "data/control.db")
	dataDir := filepath.Dir(dbPath)
	if dataDir == "." {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create data directory: %w", err)
	}

	cfg := Config{
		Path:                  configPath,
		Addr:                  configValue(values, "CONTROL_ADDR", "127.0.0.1:8080"),
		DBPath:                dbPath,
		PublicBaseURL:         strings.TrimRight(configValue(values, "CONTROL_PUBLIC_BASE_URL", "https://con.xuda.live"), "/"),
		WebDir:                configValue(values, "CONTROL_WEB_DIR", ""),
		AdminKey:              configValue(values, "CONTROL_ADMIN_KEY", "change-me"),
		EventRetentionDays:    intConfigValue(values, "CONTROL_EVENT_RETENTION_DAYS", 90),
		AuditRetentionDays:    intConfigValue(values, "CONTROL_AUDIT_RETENTION_DAYS", 30),
		OverviewScalePercent:  intConfigValue(values, "CONTROL_OVERVIEW_SCALE_PERCENT", 100),
		RiskLoginWindow:       durationConfigValue(values, "CONTROL_RISK_LOGIN_WINDOW", 10*time.Minute),
		RiskLoginCount:        intConfigValue(values, "CONTROL_RISK_LOGIN_COUNT", 5),
		RiskDeviceCount:       intConfigValue(values, "CONTROL_RISK_DEVICE_COUNT", 5),
		COSRegion:             configValue(values, "CONTROL_COS_REGION", ""),
		COSBucket:             configValue(values, "CONTROL_COS_BUCKET", ""),
		COSSecretID:           configValue(values, "CONTROL_COS_SECRET_ID", ""),
		COSSecretKey:          configValue(values, "CONTROL_COS_SECRET_KEY", ""),
		COSWebsiteBucket:      configValue(values, "CONTROL_COS_WEBSITE_BUCKET", ""),
		CDNDomain:             strings.TrimRight(configValue(values, "CONTROL_CDN_DOMAIN", ""), "/"),
		CDNAuthKey:            configValue(values, "CONTROL_CDN_AUTH_KEY", ""),
		CDNURLTTL:             durationConfigValue(values, "CONTROL_CDN_URL_TTL", 30*time.Minute),
		MonitorSecretID:       configValue(values, "CONTROL_MONITOR_SECRET_ID", ""),
		MonitorSecretKey:      configValue(values, "CONTROL_MONITOR_SECRET_KEY", ""),
		IngestCourses:         ingestSwitch(values, SettingAcceptCourses),
		IngestTextbooks:       ingestSwitch(values, SettingAcceptTextbooks),
		IngestSubmissions:     ingestSwitch(values, SettingAcceptSubmissions),
		IngestUserInfo:        ingestSwitch(values, SettingAcceptUserInfo),
		IngestEvents:          ingestSwitch(values, SettingAcceptEvents),
		AIAPIKey:              strings.TrimSpace(values["CONTROL_AI_API_KEY"]),
		AIBaseURL:             configValue(values, "CONTROL_AI_BASE_URL", "https://api.deepseek.com/v1"),
		AIModel:               configValue(values, "CONTROL_AI_MODEL", "deepseek-flash"),
		AIWindows:             configValue(values, "CONTROL_AI_WINDOWS", "00:00-08:50,12:00-13:50,18:00-24:00"),
		AITimezone:            configValue(values, "CONTROL_AI_TIMEZONE", "Asia/Shanghai"),
		AIRequestTimeout:      durationConfigValue(values, "CONTROL_AI_REQUEST_TIMEOUT", 10*time.Minute),
		AIMaxAttempts:         intConfigValue(values, "CONTROL_AI_MAX_ATTEMPTS", 3),
		AIImageQuality:        intConfigValue(values, "CONTROL_AI_IMAGE_QUALITY", 80),
		AIImageMaxEdge:        intConfigValue(values, "CONTROL_AI_IMAGE_MAX_EDGE", 1600),
		AIWorkDir:             configValue(values, "CONTROL_AI_WORK_DIR", filepath.Join(filepath.Dir(dbPath), "ai-work")),
		AIMathJaxDir:          configValue(values, "CONTROL_AI_MATHJAX_DIR", "ai-math"),
		AIPrecheckSystem:      configValue(values, SettingAIPrecheckSystem, ai.DefaultPrecheckSystem),
		AIAnalysisSystem:      configValue(values, SettingAIAnalysisSystem, ai.DefaultAnalysisSystem),
		AIPrecheckInstruction: configValue(values, SettingAIPrecheckInstruction, ai.DefaultPrecheckInstruction),
		AIAnalysisInstruction: configValue(values, SettingAIAnalysisInstruction, ai.DefaultAnalysisInstruction),
	}
	// AI is enabled whenever a key is configured; there is no separate switch.
	cfg.AIAnalysisEnabled = strings.TrimSpace(cfg.AIAPIKey) != ""
	if cfg.AIMaxAttempts < 1 || cfg.AIMaxAttempts > 10 {
		cfg.AIMaxAttempts = 3
	}
	if cfg.AIImageQuality < 10 || cfg.AIImageQuality > 100 {
		cfg.AIImageQuality = 80
	}
	if cfg.AIImageMaxEdge < 320 || cfg.AIImageMaxEdge > 4096 {
		cfg.AIImageMaxEdge = 1600
	}
	if cfg.EventRetentionDays < 1 {
		cfg.EventRetentionDays = 90
	}
	if cfg.AuditRetentionDays < 1 {
		cfg.AuditRetentionDays = 30
	}
	if cfg.OverviewScalePercent < 0 || cfg.OverviewScalePercent > 100000 {
		cfg.OverviewScalePercent = 100
	}
	if cfg.RiskLoginCount < 1 {
		cfg.RiskLoginCount = 5
	}
	if cfg.RiskDeviceCount < 1 {
		cfg.RiskDeviceCount = 5
	}

	cfg.TokenPepper, err = secret(values, "CONTROL_TOKEN_PEPPER")
	if err != nil {
		return Config{}, err
	}
	cfg.IDPepper, err = secret(values, "CONTROL_ID_PEPPER")
	if err != nil {
		return Config{}, err
	}
	cfg.EncryptionKey, err = encryptionKey(values, "CONTROL_ENCRYPTION_KEY")
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ParseAdminPerms(raw string) (map[string]string, error) {
	perms := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return perms, nil
	}
	if raw == "*" {
		perms["*"] = "rw"
		return perms, nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pieces := strings.SplitN(part, "=", 2)
		if len(pieces) != 2 {
			return nil, fmt.Errorf("invalid permission %q, want page=r or page=rw", part)
		}
		page := strings.ToLower(strings.TrimSpace(pieces[0]))
		access := strings.ToLower(strings.TrimSpace(pieces[1]))
		if access != "r" && access != "rw" {
			return nil, fmt.Errorf("invalid access %q for page %q", access, page)
		}
		if page == "*" {
			if access != "rw" {
				return nil, fmt.Errorf("wildcard permission only supports rw")
			}
			perms["*"] = "rw"
			continue
		}
		if !isAdminPage(page) {
			return nil, fmt.Errorf("unknown page %q", page)
		}
		perms[page] = access
	}
	return perms, nil
}

func isAdminPage(page string) bool {
	for _, candidate := range AdminPages {
		if candidate == page {
			return true
		}
	}
	return false
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func loadDotEnv(path string) (map[string]string, error) {
	values := make(map[string]string)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		values, err := defaultDotEnvValues()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create config directory: %w", err)
		}
		if err := writeDotEnv(path, values); err != nil {
			return nil, fmt.Errorf("create config file %s: %w", path, err)
		}
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}
	for lineNumber, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			return nil, fmt.Errorf("invalid config line %d in %s", lineNumber+1, path)
		}
		key := strings.TrimSpace(line[:separator])
		if !envKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid config key %q on line %d in %s", key, lineNumber+1, path)
		}
		values[key] = parseDotEnvValue(strings.TrimSpace(line[separator+1:]))
	}
	return values, nil
}

func defaultDotEnvValues() (map[string]string, error) {
	tokenPepper, err := newBase64Secret(32)
	if err != nil {
		return nil, err
	}
	idPepper, err := newBase64Secret(32)
	if err != nil {
		return nil, err
	}
	encryptionKey, err := newBase64Secret(32)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"CONTROL_ADDR":                   "127.0.0.1:8080",
		"CONTROL_DB":                     "data/control.db",
		"CONTROL_PUBLIC_BASE_URL":        "https://con.xuda.live",
		"CONTROL_WEB_DIR":                "",
		"CONTROL_TOKEN_PEPPER":           tokenPepper,
		"CONTROL_ID_PEPPER":              idPepper,
		"CONTROL_ENCRYPTION_KEY":         encryptionKey,
		"CONTROL_ADMIN_KEY":              "change-me",
		"CONTROL_EVENT_RETENTION_DAYS":   "90",
		"CONTROL_AUDIT_RETENTION_DAYS":   "30",
		"CONTROL_OVERVIEW_SCALE_PERCENT": "100",
		"CONTROL_RISK_LOGIN_WINDOW":      "10m",
		"CONTROL_RISK_LOGIN_COUNT":       "5",
		"CONTROL_RISK_DEVICE_COUNT":      "5",
	}, nil
}

func ensureSecretValues(path string, values map[string]string) error {
	updates := make(map[string]string)
	for _, key := range []string{"CONTROL_TOKEN_PEPPER", "CONTROL_ID_PEPPER", "CONTROL_ENCRYPTION_KEY"} {
		if strings.TrimSpace(values[key]) != "" {
			continue
		}
		value, err := newBase64Secret(32)
		if err != nil {
			return fmt.Errorf("generate %s: %w", key, err)
		}
		values[key] = value
		updates[key] = value
	}
	if len(updates) == 0 {
		return nil
	}
	return updateDotEnv(path, updates)
}

func newBase64Secret(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate random config secret: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(data), nil
}

func writeDotEnv(path string, values map[string]string) error {
	keys := []string{
		"CONTROL_ADDR",
		"CONTROL_DB",
		"CONTROL_PUBLIC_BASE_URL",
		"CONTROL_WEB_DIR",
		"CONTROL_TOKEN_PEPPER",
		"CONTROL_ID_PEPPER",
		"CONTROL_ENCRYPTION_KEY",
		"CONTROL_ADMIN_KEY",
		"CONTROL_EVENT_RETENTION_DAYS",
		"CONTROL_AUDIT_RETENTION_DAYS",
		"CONTROL_OVERVIEW_SCALE_PERCENT",
		"CONTROL_RISK_LOGIN_WINDOW",
		"CONTROL_RISK_LOGIN_COUNT",
		"CONTROL_RISK_DEVICE_COUNT",
	}
	var builder strings.Builder
	builder.WriteString("# xzitpocket-control configuration\n")
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(values[key])
		builder.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

func updateDotEnv(path string, updates map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	updated := make(map[string]bool, len(updates))
	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "export ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		}
		separator := strings.IndexByte(trimmed, '=')
		if separator <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:separator])
		value, ok := updates[key]
		if !ok {
			continue
		}
		lines[i] = key + "=" + value
		updated[key] = true
	}
	for key, value := range updates {
		if !updated[key] {
			lines = append(lines, key+"="+value)
		}
	}
	return os.WriteFile(path, []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n")+"\n"), 0o600)
}

func parseDotEnvValue(value string) string {
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				return unquoted
			}
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return value[1 : len(value)-1]
		}
	}
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = value[:comment]
	}
	return strings.TrimSpace(value)
}

func configValue(values map[string]string, key, fallback string) string {
	if value := strings.TrimSpace(values[key]); value != "" {
		return value
	}
	return fallback
}

// IngestSwitch is one ingestion toggle. The zero value means "accept": a
// configuration that never mentions the switch keeps ingesting, which matters
// for tests and for hand-written configs.
type IngestSwitch struct{ Disabled bool }

// Enabled reports whether reports of this kind are stored.
func (s IngestSwitch) Enabled() bool { return !s.Disabled }

// ingestSwitch reads a bootstrap value ("true" = accept).
func ingestSwitch(values map[string]string, key string) IngestSwitch {
	raw, ok := values[key]
	if !ok {
		return IngestSwitch{}
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return IngestSwitch{}
	}
	return IngestSwitch{Disabled: !value}
}

func intConfigValue(values map[string]string, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil {
		return fallback
	}
	return value
}

func durationConfigValue(values map[string]string, key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return fallback
	}
	result, err := time.ParseDuration(value)
	if err != nil || result <= 0 {
		return fallback
	}
	return result
}

func secret(values map[string]string, key string) ([]byte, error) {
	if value := strings.TrimSpace(values[key]); value != "" {
		if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil && len(decoded) >= 32 {
			return decoded, nil
		}
		return nil, fmt.Errorf("%s must be base64 without padding and decode to at least 32 bytes", key)
	}
	return nil, fmt.Errorf("%s must be set in data/.env", key)
}

func encryptionKey(values map[string]string, key string) ([]byte, error) {
	if value := strings.TrimSpace(values[key]); value != "" {
		decoded, err := base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("%s must be base64 without padding: %w", key, err)
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("%s must decode to 32 bytes", key)
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("%s must be set in data/.env", key)
}
