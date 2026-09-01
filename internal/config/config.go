package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                   string
	DBPath                 string
	PublicBaseURL          string
	TokenPepper            []byte
	IDPepper               []byte
	EncryptionKey          []byte
	ServiceSigningKeyPath  string
	AdminBootstrap         string
	EventRetentionDays     int
	RiskLoginWindow        time.Duration
	RiskLoginCount         int
	RiskDeviceCount        int
	GeneratedSecretWarning []string
}

func Load() (Config, error) {
	dbPath := env("CONTROL_DB", "data/control.db")
	dataDir := filepath.Dir(dbPath)
	if dataDir == "." {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create data directory: %w", err)
	}

	cfg := Config{
		Addr:                  env("CONTROL_ADDR", "127.0.0.1:8080"),
		DBPath:                dbPath,
		PublicBaseURL:         strings.TrimRight(env("CONTROL_PUBLIC_BASE_URL", "http://127.0.0.1:8080"), "/"),
		ServiceSigningKeyPath: env("CONTROL_SERVICE_SIGNING_KEY", filepath.Join(dataDir, "service-signing.key")),
		AdminBootstrap:        env("CONTROL_ADMIN_BOOTSTRAP", "change-me"),
		EventRetentionDays:    intEnv("CONTROL_EVENT_RETENTION_DAYS", 90),
		RiskLoginWindow:       durationEnv("CONTROL_RISK_LOGIN_WINDOW", 10*time.Minute),
		RiskLoginCount:        intEnv("CONTROL_RISK_LOGIN_COUNT", 5),
		RiskDeviceCount:       intEnv("CONTROL_RISK_DEVICE_COUNT", 5),
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

	var err error
	cfg.TokenPepper, err = secret("CONTROL_TOKEN_PEPPER", filepath.Join(dataDir, "token.pepper"))
	if err != nil {
		return Config{}, err
	}
	cfg.IDPepper, err = secret("CONTROL_ID_PEPPER", filepath.Join(dataDir, "id.pepper"))
	if err != nil {
		return Config{}, err
	}
	cfg.EncryptionKey, err = encryptionKey("CONTROL_ENCRYPTION_KEY", filepath.Join(dataDir, "encryption.key"))
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	result, err := time.ParseDuration(value)
	if err != nil || result <= 0 {
		return fallback
	}
	return result
}

func secret(envName, path string) ([]byte, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil && len(decoded) >= 32 {
			return decoded, nil
		}
		return []byte(value), nil
	}
	return loadOrCreate(path, 32)
}

func encryptionKey(envName, path string) ([]byte, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		decoded, err := base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("%s must be base64 without padding: %w", envName, err)
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("%s must decode to 32 bytes", envName)
		}
		return decoded, nil
	}
	return loadOrCreate(path, 32)
}

func loadOrCreate(path string, size int) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		decoded, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr == nil && len(decoded) == size {
			return decoded, nil
		}
		return nil, fmt.Errorf("invalid secret file %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret file %s: %w", path, err)
	}

	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(data)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write secret file %s: %w", path, err)
	}
	return data, nil
}
