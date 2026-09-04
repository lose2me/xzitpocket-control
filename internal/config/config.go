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
)

type Config struct {
	Addr               string
	DBPath             string
	PublicBaseURL      string
	TokenPepper        []byte
	IDPepper           []byte
	EncryptionKey      []byte
	AdminKey           string
	EventRetentionDays int
	RiskLoginWindow    time.Duration
	RiskLoginCount     int
	RiskDeviceCount    int
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
		Addr:               configValue(values, "CONTROL_ADDR", "127.0.0.1:8080"),
		DBPath:             dbPath,
		PublicBaseURL:      strings.TrimRight(configValue(values, "CONTROL_PUBLIC_BASE_URL", "https://con.xuda.live"), "/"),
		AdminKey:           configValue(values, "CONTROL_ADMIN_KEY", "change-me"),
		EventRetentionDays: intConfigValue(values, "CONTROL_EVENT_RETENTION_DAYS", 90),
		RiskLoginWindow:    durationConfigValue(values, "CONTROL_RISK_LOGIN_WINDOW", 10*time.Minute),
		RiskLoginCount:     intConfigValue(values, "CONTROL_RISK_LOGIN_COUNT", 5),
		RiskDeviceCount:    intConfigValue(values, "CONTROL_RISK_DEVICE_COUNT", 5),
	}
	if cfg.EventRetentionDays < 1 {
		cfg.EventRetentionDays = 90
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
		"CONTROL_ADDR":                 "127.0.0.1:8080",
		"CONTROL_DB":                   "data/control.db",
		"CONTROL_PUBLIC_BASE_URL":      "https://con.xuda.live",
		"CONTROL_TOKEN_PEPPER":         tokenPepper,
		"CONTROL_ID_PEPPER":            idPepper,
		"CONTROL_ENCRYPTION_KEY":       encryptionKey,
		"CONTROL_ADMIN_KEY":            "change-me",
		"CONTROL_EVENT_RETENTION_DAYS": "90",
		"CONTROL_RISK_LOGIN_WINDOW":    "10m",
		"CONTROL_RISK_LOGIN_COUNT":     "5",
		"CONTROL_RISK_DEVICE_COUNT":    "5",
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
		"CONTROL_TOKEN_PEPPER",
		"CONTROL_ID_PEPPER",
		"CONTROL_ENCRYPTION_KEY",
		"CONTROL_ADMIN_KEY",
		"CONTROL_EVENT_RETENTION_DAYS",
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
