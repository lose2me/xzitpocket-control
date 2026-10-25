package config

import (
	"strings"
	"testing"
	"time"
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
		PublicBaseURL:        "https://con.example.com/",
		EventRetentionDays:   45,
		AuditRetentionDays:   7,
		OverviewScalePercent: 150,
		RiskLoginWindow:      3 * time.Minute,
		RiskLoginCount:       9,
		RiskDeviceCount:      4,
		COSRegion:            "ap-shanghai",
		COSBucket:            "assets",
		COSSecretID:          "id",
		COSSecretKey:         "key",
		COSWebsiteBucket:     "site",
		CDNDomain:            "https://dl.example.com/",
		CDNAuthKey:           "cdn-key",
		CDNURLTTL:            time.Hour,
		MonitorSecretID:      "mid",
		MonitorSecretKey:     "mkey",
		AIAnalysisEnabled:    true,
		AIAPIKey:             "ai-key",
		AIBaseURL:            "https://api.example.com/v1/",
		AIModel:              "model",
		AIWindows:            "00:00-08:50",
		AITimezone:           "Asia/Shanghai",
		AIPagesPerChunk:      10,
		AIRequestTimeout:     90 * time.Second,
		AIMaxAttempts:        2,
		AIImageQuality:       70,
		AIImageMaxEdge:       1200,
	}
	var parsed Config
	if err := ApplyRuntime(&parsed, RuntimeValues(source)); err != nil {
		t.Fatal(err)
	}
	if parsed.PublicBaseURL != "https://con.example.com" || parsed.CDNDomain != "https://dl.example.com" {
		t.Fatalf("urls not normalized: %q %q", parsed.PublicBaseURL, parsed.CDNDomain)
	}
	if parsed.EventRetentionDays != 45 || parsed.RiskLoginCount != 9 || parsed.AIPagesPerChunk != 10 {
		t.Fatalf("numbers not applied: %+v", parsed)
	}
	if parsed.RiskLoginWindow != 3*time.Minute || parsed.CDNURLTTL != time.Hour || parsed.AIRequestTimeout != 90*time.Second {
		t.Fatalf("durations not applied: %+v", parsed)
	}
	if !parsed.AIAnalysisEnabled || parsed.AIAPIKey != "ai-key" || parsed.AIImageMaxEdge != 1200 {
		t.Fatalf("ai settings not applied: %+v", parsed)
	}
}

func TestApplyRuntimeRejectsInvalid(t *testing.T) {
	for key, value := range map[string]string{
		SettingEventRetentionDays:   "0",
		SettingOverviewScalePercent: "-1",
		SettingRiskLoginWindow:      "soon",
		SettingAIEnabled:            "maybe",
		SettingAIPagesPerChunk:      "999",
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
