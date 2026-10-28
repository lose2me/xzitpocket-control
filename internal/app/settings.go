package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
)

// settingSecretPrefix marks values encrypted with CONTROL_ENCRYPTION_KEY. Cloud
// credentials (and the AI key) must not be readable from a database backup.
const settingSecretPrefix = "enc:v1:"

// SettingsFieldView is one editable runtime setting shown in the console.
type SettingsFieldView struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Hint  string `json:"hint,omitempty"`
	Value string `json:"value"`
	Set   bool   `json:"set"`
	Min   int    `json:"min,omitempty"`
	Max   int    `json:"max,omitempty"`
}

// SettingsGroupView groups fields for the console form.
type SettingsGroupView struct {
	Key    string              `json:"key"`
	Label  string              `json:"label"`
	Fields []SettingsFieldView `json:"fields"`
}

// SettingsView is the console payload. Secrets are never returned: Kind is
// "secret" and Set reports whether a value is stored.
type SettingsView struct {
	Groups []SettingsGroupView `json:"groups"`
}

// SettingsUpdate is the PUT payload. Secret keys left empty in Values keep the
// stored value; list a key in Clear to remove it.
type SettingsUpdate struct {
	Values map[string]string `json:"values"`
	Clear  []string          `json:"clear"`
}

// LoadSettings seeds missing rows from the .env values loaded at boot, then
// applies the stored settings on top of the running configuration.
func (a *App) LoadSettings(ctx context.Context) error {
	seed := make(map[string]string, len(config.SettingSpecs))
	for key, value := range config.RuntimeValues(a.cfg()) {
		// Only seed values that pass validation: a test binary or a hand-edited
		// .env may hold zeros that must not become stored settings.
		probe := a.cfg()
		if err := config.ApplyRuntime(&probe, map[string]string{key: value}); err != nil {
			a.Logger.Warn("skipping invalid .env setting", "key", key, "error", err)
			continue
		}
		stored, err := a.sealSetting(key, value)
		if err != nil {
			return err
		}
		seed[key] = stored
	}
	if err := a.Store.SeedSettings(ctx, seed, time.Now().UTC()); err != nil {
		return err
	}
	stored, err := a.Store.Settings(ctx)
	if err != nil {
		return err
	}
	values := make(map[string]string, len(stored))
	for key, value := range stored {
		plain, err := a.openSetting(key, value)
		if err != nil {
			a.Logger.Warn("stored setting cannot be decrypted; keeping the .env value", "key", key, "error", err)
			continue
		}
		values[key] = plain
	}
	next := a.cfg()
	for key, value := range values {
		probe := next
		if err := config.ApplyRuntime(&probe, map[string]string{key: value}); err != nil {
			a.Logger.Warn("stored setting is invalid; keeping the current value", "key", key, "error", err)
			continue
		}
		next = probe
	}
	a.cfgMu.Lock()
	a.Cfg = next
	a.cfgMu.Unlock()
	return nil
}

// SettingsView renders every editable setting for the console.
func (a *App) SettingsView() SettingsView {
	plain := config.RuntimeValues(a.cfg())
	groups := make([]SettingsGroupView, 0, len(config.SettingGroups))
	index := make(map[string]int, len(config.SettingGroups))
	for _, group := range config.SettingGroups {
		index[group.Key] = len(groups)
		groups = append(groups, SettingsGroupView{Key: group.Key, Label: group.Label})
	}
	for _, spec := range config.SettingSpecs {
		position, ok := index[spec.Group]
		if !ok {
			continue
		}
		field := SettingsFieldView{
			Key: spec.Key, Label: spec.Label, Kind: spec.Kind, Hint: spec.Hint,
			Min: spec.Min, Max: spec.Max,
		}
		if spec.Kind == config.SettingKindSecret {
			field.Set = strings.TrimSpace(plain[spec.Key]) != ""
		} else {
			field.Value = plain[spec.Key]
		}
		groups[position].Fields = append(groups[position].Fields, field)
	}
	return SettingsView{Groups: groups}
}

// UpdateSettings validates and persists runtime settings, applies them to the
// running process (including the AI worker) and writes an audit entry.
func (a *App) UpdateSettings(ctx context.Context, in SettingsUpdate, actor string) (SettingsView, error) {
	updates, err := a.normalizeSettingsUpdate(in)
	if err != nil {
		return SettingsView{}, err
	}
	if len(updates) == 0 {
		return a.SettingsView(), nil
	}
	if err := a.applyRuntimeUpdates(ctx, updates); err != nil {
		return SettingsView{}, err
	}
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	if err := a.Store.AddAudit(ctx, actor, "settings_update", "settings", "", controlcrypto.JSON(map[string][]string{"keys": keys}), time.Now().UTC()); err != nil {
		a.Logger.Warn("audit settings update", "error", err)
	}
	return a.SettingsView(), nil
}

// normalizeSettingsUpdate validates keys and drops empty secret values, which
// mean "keep the stored value".
func (a *App) normalizeSettingsUpdate(in SettingsUpdate) (map[string]string, error) {
	updates := make(map[string]string, len(in.Values)+len(in.Clear))
	for key, value := range in.Values {
		spec, ok := config.SettingSpecByKey(key)
		if !ok {
			return nil, a.invalidSetting("未知的配置项：" + key)
		}
		value = strings.TrimSpace(value)
		if spec.Kind == config.SettingKindSecret && value == "" {
			continue
		}
		updates[key] = value
	}
	for _, key := range in.Clear {
		if _, ok := config.SettingSpecByKey(key); !ok {
			return nil, a.invalidSetting("未知的配置项：" + key)
		}
		updates[key] = ""
	}
	return updates, nil
}

// applyRuntimeUpdates persists the given values (encrypting secrets) and swaps
// the running configuration and the AI snapshot.
func (a *App) applyRuntimeUpdates(ctx context.Context, updates map[string]string) error {
	next := a.cfg()
	if err := config.ApplyRuntime(&next, updates); err != nil {
		return a.invalidSetting(err.Error())
	}
	state, err := aiStateFromConfig(next, a.Logger)
	if err != nil {
		return a.invalidSetting(err.Error())
	}
	stored := make(map[string]string, len(updates))
	for key, value := range updates {
		sealed, err := a.sealSetting(key, value)
		if err != nil {
			return err
		}
		stored[key] = sealed
	}
	if err := a.Store.UpdateSettings(ctx, stored, time.Now().UTC()); err != nil {
		return err
	}
	a.cfgMu.Lock()
	a.Cfg = next
	a.cfgMu.Unlock()
	a.aiState.Store(state)
	a.wakeAIWorker()
	return nil
}

// sealSetting encrypts secret settings before they are written to SQLite.
func (a *App) sealSetting(key, value string) (string, error) {
	spec, ok := config.SettingSpecByKey(key)
	if !ok || spec.Kind != config.SettingKindSecret || value == "" {
		return value, nil
	}
	ciphertext, err := controlcrypto.Encrypt(a.cfg().EncryptionKey, value)
	if err != nil {
		return "", err
	}
	return settingSecretPrefix + ciphertext, nil
}

// openSetting reverses sealSetting for current encrypted secret values.
func (a *App) openSetting(key, value string) (string, error) {
	spec, ok := config.SettingSpecByKey(key)
	if !ok || spec.Kind != config.SettingKindSecret || value == "" {
		return value, nil
	}
	plain, err := controlcrypto.Decrypt(a.cfg().EncryptionKey, strings.TrimPrefix(value, settingSecretPrefix))
	if err != nil {
		return "", err
	}
	return plain, nil
}

func (a *App) invalidSetting(message string) *APIError {
	return Err("invalid_settings", message, http.StatusBadRequest)
}
