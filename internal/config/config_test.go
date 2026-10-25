package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# comment\nTEST_DOTENV_PLAIN=plain\nTEST_DOTENV_QUOTED=\"quoted value\"\nexport TEST_DOTENV_SINGLE='single value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := loadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["TEST_DOTENV_PLAIN"]; got != "plain" {
		t.Fatalf("file value was not loaded: %q", got)
	}
	if got := values["TEST_DOTENV_QUOTED"]; got != "quoted value" {
		t.Fatalf("quoted value = %q", got)
	}
	if got := values["TEST_DOTENV_SINGLE"]; got != "single value" {
		t.Fatalf("single-quoted value = %q", got)
	}
}

func TestLoadDotEnvMissingIsCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", ".env")
	values, err := loadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CONTROL_TOKEN_PEPPER", "CONTROL_ID_PEPPER", "CONTROL_ENCRYPTION_KEY"} {
		if len(values[key]) == 0 {
			t.Fatalf("generated config value %s is empty", key)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("created config file is missing: %v", err)
	}
	reloaded, err := loadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded["CONTROL_TOKEN_PEPPER"] != values["CONTROL_TOKEN_PEPPER"] {
		t.Fatal("generated token pepper was not persisted")
	}
}

func TestEnsureSecretValuesFillsEmptyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("CONTROL_TOKEN_PEPPER=\nCONTROL_ID_PEPPER=\nCONTROL_ENCRYPTION_KEY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := loadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureSecretValues(path, values); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CONTROL_TOKEN_PEPPER", "CONTROL_ID_PEPPER", "CONTROL_ENCRYPTION_KEY"} {
		if len(values[key]) == 0 {
			t.Fatalf("filled config value %s is empty", key)
		}
	}
	reloaded, err := loadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CONTROL_TOKEN_PEPPER", "CONTROL_ID_PEPPER", "CONTROL_ENCRYPTION_KEY"} {
		if reloaded[key] != values[key] {
			t.Fatalf("filled config value %s was not persisted", key)
		}
	}
}
