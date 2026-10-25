package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/ai"
	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/store/sqlite"
)

func settingsField(t *testing.T, view SettingsView, key string) SettingsFieldView {
	t.Helper()
	for _, group := range view.Groups {
		for _, field := range group.Fields {
			if field.Key == key {
				return field
			}
		}
	}
	t.Fatalf("setting %s missing from view", key)
	return SettingsFieldView{}
}

// TestUpdateSettingsHotApplies covers the store + runtime path: values apply
// immediately, secrets are encrypted at rest and never echoed, empty secrets
// keep the stored value, and invalid input changes nothing.
func TestUpdateSettingsHotApplies(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()

	view, err := a.UpdateSettings(ctx, SettingsUpdate{Values: map[string]string{
		config.SettingEventRetentionDays: "45",
		config.SettingCOSSecretKey:       "super-secret-key",
	}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.cfg().EventRetentionDays; got != 45 {
		t.Fatalf("retention = %d, want 45 without restart", got)
	}
	if got := a.cfg().COSSecretKey; got != "super-secret-key" {
		t.Fatalf("secret not applied: %q", got)
	}
	field := settingsField(t, view, config.SettingCOSSecretKey)
	if field.Value != "" || !field.Set {
		t.Fatalf("secret leaked to the console: %#v", field)
	}
	if settingsField(t, view, config.SettingEventRetentionDays).Value != "45" {
		t.Fatalf("view did not reflect the saved value")
	}

	stored, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := stored[config.SettingCOSSecretKey]
	if !strings.HasPrefix(raw, settingSecretPrefix) || strings.Contains(raw, "super-secret-key") {
		t.Fatalf("secret is not encrypted at rest: %q", raw)
	}

	// An empty secret means "keep the stored value".
	if _, err := a.UpdateSettings(ctx, SettingsUpdate{Values: map[string]string{
		config.SettingCOSSecretKey: "",
	}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := a.cfg().COSSecretKey; got != "super-secret-key" {
		t.Fatalf("empty secret cleared the value: %q", got)
	}

	// Invalid values are rejected before anything is persisted.
	if _, err := a.UpdateSettings(ctx, SettingsUpdate{Values: map[string]string{
		config.SettingEventRetentionDays: "0",
	}}, "admin"); err == nil {
		t.Fatal("invalid retention was accepted")
	}
	if got := a.cfg().EventRetentionDays; got != 45 {
		t.Fatalf("rejected update still changed the runtime config: %d", got)
	}
	if _, err := a.UpdateSettings(ctx, SettingsUpdate{Values: map[string]string{
		"CONTROL_UNKNOWN_KEY": "x",
	}}, "admin"); err == nil {
		t.Fatal("unknown setting was accepted")
	}

	// A restart must rebuild the same runtime values from the database.
	reloaded, err := New(config.Config{
		TokenPepper:   a.cfg().TokenPepper,
		IDPepper:      a.cfg().IDPepper,
		EncryptionKey: a.cfg().EncryptionKey,
		AdminKey:      "test",
	}, store, a.Logger)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.cfg().EventRetentionDays != 45 || reloaded.cfg().COSSecretKey != "super-secret-key" {
		t.Fatalf("settings were not restored from the database: retention=%d key=%q",
			reloaded.cfg().EventRetentionDays, reloaded.cfg().COSSecretKey)
	}
}

// TestLoadSettingsSeedsFromConfig covers an upgrade from a .env-only install:
// the first start copies the .env values into app_settings, encrypting secrets.
func TestLoadSettingsSeedsFromConfig(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	a, err := New(config.Config{
		TokenPepper:        []byte("token"),
		IDPepper:           []byte("id"),
		EncryptionKey:      key,
		AdminKey:           "test",
		EventRetentionDays: 21,
		COSSecretKey:       "seed-secret",
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg().EventRetentionDays != 21 || a.cfg().COSSecretKey != "seed-secret" {
		t.Fatalf("seeded config not applied: %+v", a.cfg())
	}
	stored, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored[config.SettingEventRetentionDays] != "21" {
		t.Fatalf("retention not seeded: %#v", stored)
	}
	if raw := stored[config.SettingCOSSecretKey]; !strings.HasPrefix(raw, settingSecretPrefix) || strings.Contains(raw, "seed-secret") {
		t.Fatalf("seeded secret is not encrypted: %q", raw)
	}
}

// TestAIEnablementFollowsAPIKey covers the removal of the enable switch: a
// configured key starts the worker, clearing the key stops it.
func TestAIEnablementFollowsAPIKey(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()

	if _, err := a.UpdateSettings(ctx, SettingsUpdate{Values: map[string]string{
		config.SettingAIAPIKey:         "sk-test",
		config.SettingAIBaseURL:        "https://api.example.com/v1",
		config.SettingAIModel:          "test-model",
		config.SettingAIWindows:        "00:00-24:00",
		config.SettingAITimezone:       "UTC",
		config.SettingAIPagesPerChunk:  "30",
		config.SettingAIRequestTimeout: "10m",
		config.SettingAIMaxAttempts:    "3",
		config.SettingAIImageQuality:   "80",
		config.SettingAIImageMaxEdge:   "1600",
	}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if !a.cfg().AIAnalysisEnabled || a.ai().model == nil {
		t.Fatalf("AI should be enabled once a key is stored: %+v", a.cfg().AIAnalysisEnabled)
	}

	if _, err := a.UpdateSettings(ctx, SettingsUpdate{Clear: []string{config.SettingAIAPIKey}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if a.cfg().AIAnalysisEnabled || a.ai().model != nil {
		t.Fatal("AI must stop when the API key is cleared")
	}
}

// TestLoadSettingsDropsObsoleteToggle removes CONTROL_AI_ENABLED rows left in
// databases created before the switch was dropped.
func TestLoadSettingsDropsObsoleteToggle(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSettings(ctx, map[string]string{"CONTROL_AI_ENABLED": "false"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config.Config{
		TokenPepper:   []byte("token"),
		IDPepper:      []byte("id"),
		EncryptionKey: []byte("01234567890123456789012345678901"),
		AdminKey:      "test",
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stored["CONTROL_AI_ENABLED"]; ok {
		t.Fatalf("obsolete toggle was not removed: %#v", stored)
	}
}

// TestAIPromptSettingsHotApply covers the prompt settings: saving them updates
// the running config immediately and clearing falls back to the default.
func TestAIPromptSettingsHotApply(t *testing.T) {
	a, _ := newCourseApp(t)
	ctx := context.Background()

	if _, err := a.UpdateSettings(ctx, SettingsUpdate{Values: map[string]string{
		config.SettingAIChunkInstruction:    "自定义分段 %s %d-%d/%d",
		config.SettingAIPrecheckInstruction: "自定义预检 %s %d/%d",
	}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if a.cfg().AIChunkInstruction != "自定义分段 %s %d-%d/%d" || a.cfg().AIPrecheckInstruction != "自定义预检 %s %d/%d" {
		t.Fatalf("prompts not applied: %+v", a.cfg().AIChunkInstruction)
	}
	if _, err := a.UpdateSettings(ctx, SettingsUpdate{Clear: []string{config.SettingAIChunkInstruction}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if a.cfg().AIChunkInstruction != ai.DefaultChunkInstruction {
		t.Fatalf("cleared prompt must fall back to default: %q", a.cfg().AIChunkInstruction)
	}
}
