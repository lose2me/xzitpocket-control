package config

import (
	"strings"
	"testing"
	"time"

	"xzitpocket-control/internal/ai"
)

func TestRuntimeValuesCoverAllSpecs(t *testing.T) {
	values := RuntimeValues(Config{})
	if len(values) != len(SettingSpecs) {
		t.Fatalf("RuntimeValues has %d keys for %d specs", len(values), len(SettingSpecs))
	}
	for _, spec := range SettingSpecs {
		if _, ok := values[spec.Key]; !ok {
			t.Fatalf("spec %s has no runtime value", spec.Key)
		}
		if _, ok := SettingSpecByKey(spec.Key); !ok {
			t.Fatalf("spec %s is not registered", spec.Key)
		}
	}
}

func TestApplyRuntimeRoundTrip(t *testing.T) {
	source := Config{
		PublicBaseURL:         "https://con.example.com/",
		EventRetentionDays:    45,
		AuditRetentionDays:    7,
		OverviewScalePercent:  150,
		RiskLoginWindow:       3 * time.Minute,
		RiskLoginCount:        9,
		RiskDeviceCount:       4,
		COSRegion:             "ap-shanghai",
		COSBucket:             "assets",
		COSSecretID:           "id",
		COSSecretKey:          "key",
		COSWebsiteBucket:      "site",
		CDNDomain:             "https://dl.example.com/",
		CDNAuthKey:            "cdn-key",
		CDNURLTTL:             time.Hour,
		MonitorSecretID:       "mid",
		MonitorSecretKey:      "mkey",
		AIAPIKey:              "ai-key",
		AIBaseURL:             "https://api.example.com/v1/",
		AIModel:               "model",
		AIWindows:             "00:00-08:50",
		AITimezone:            "Asia/Shanghai",
		AIRequestTimeout:      90 * time.Second,
		AIMaxAttempts:         2,
		AIImageQuality:        70,
		AIImageMaxEdge:        1200,
		AIPrecheckSystem:      "自定义预检系统提示词",
		AIAnalysisSystem:      "自定义解析系统提示词",
		AIPrecheckInstruction: "预检 %s %d/%d",
		AIAnalysisInstruction: "解析 %s %d",
	}
	var parsed Config
	if err := ApplyRuntime(&parsed, RuntimeValues(source)); err != nil {
		t.Fatal(err)
	}
	if parsed.PublicBaseURL != "https://con.example.com" || parsed.CDNDomain != "https://dl.example.com" {
		t.Fatalf("urls not normalized: %q %q", parsed.PublicBaseURL, parsed.CDNDomain)
	}
	if parsed.EventRetentionDays != 45 || parsed.RiskLoginCount != 9 {
		t.Fatalf("numbers not applied: %+v", parsed)
	}
	if parsed.RiskLoginWindow != 3*time.Minute || parsed.CDNURLTTL != time.Hour || parsed.AIRequestTimeout != 90*time.Second {
		t.Fatalf("durations not applied: %+v", parsed)
	}
	if !parsed.AIAnalysisEnabled || parsed.AIAPIKey != "ai-key" || parsed.AIImageMaxEdge != 1200 {
		t.Fatalf("ai settings not applied: %+v", parsed)
	}
	if parsed.AIAnalysisInstruction != "解析 %s %d" || parsed.AIPrecheckSystem != "自定义预检系统提示词" {
		t.Fatalf("ai prompts not applied: %+v", parsed)
	}
}

func TestAcceptSwitchesDefaultOnAndParse(t *testing.T) {
	cfg := Config{}
	values := RuntimeValues(cfg)
	for _, key := range []string{SettingAcceptCourses, SettingAcceptTextbooks, SettingAcceptSubmissions, SettingAcceptUserInfo, SettingAcceptEvents} {
		if values[key] != "true" {
			t.Fatalf("%s default = %q, want true for a zero value", key, values[key])
		}
	}
	if err := ApplyRuntime(&cfg, map[string]string{
		SettingAcceptCourses:     "true",
		SettingAcceptSubmissions: "false",
		SettingAcceptEvents:      "1",
	}); err != nil {
		t.Fatal(err)
	}
	if !cfg.IngestCourses.Enabled() || cfg.IngestSubmissions.Enabled() || !cfg.IngestEvents.Enabled() {
		t.Fatalf("switches not applied: %#v", cfg)
	}
	if err := ApplyRuntime(&cfg, map[string]string{SettingAcceptCourses: "yes-please"}); err == nil {
		t.Fatal("a non-boolean value must be rejected")
	}
}

func TestRuntimeDerivesAIEnabledFromKey(t *testing.T) {
	var cfg Config
	if err := ApplyRuntime(&cfg, map[string]string{SettingAIAPIKey: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	if !cfg.AIAnalysisEnabled {
		t.Fatal("a configured API key must enable AI")
	}
	if err := ApplyRuntime(&cfg, map[string]string{SettingAIAPIKey: ""}); err != nil {
		t.Fatal(err)
	}
	if cfg.AIAnalysisEnabled {
		t.Fatal("clearing the API key must disable AI")
	}
}

func TestPromptsFallBackToDefaultsWhenBlank(t *testing.T) {
	var cfg Config
	if err := ApplyRuntime(&cfg, map[string]string{
		SettingAIAnalysisInstruction: "  ",
		SettingAIPrecheckInstruction: "",
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.AIAnalysisInstruction != ai.DefaultAnalysisInstruction || cfg.AIPrecheckInstruction != ai.DefaultPrecheckInstruction {
		t.Fatalf("blank prompts must fall back to the built-in templates: %+v", cfg.AIAnalysisInstruction)
	}
}

func TestApplyRuntimeRejectsInvalid(t *testing.T) {
	for key, value := range map[string]string{
		SettingEventRetentionDays:   "0",
		SettingOverviewScalePercent: "-1",
		SettingRiskLoginWindow:      "soon",
		SettingAIImageMaxEdge:       "10",
	} {
		cfg := Config{}
		if err := ApplyRuntime(&cfg, map[string]string{key: value}); err == nil {
			t.Fatalf("ApplyRuntime accepted %s=%q", key, value)
		} else if !strings.Contains(err.Error(), key) {
			t.Fatalf("error for %s does not name the key: %v", key, err)
		}
	}
}
