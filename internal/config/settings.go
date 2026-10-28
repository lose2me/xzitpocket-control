package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"xzitpocket-control/internal/ai"
)

// Setting keys stored in the database. Bootstrap settings (listen address,
// database path, web directory, the three peppers/encryption key, the super
// admin key and the AI work directories) stay in data/.env because the process
// needs them before the database is open.
const (
	SettingPublicBaseURL            = "CONTROL_PUBLIC_BASE_URL"
	SettingEventRetentionDays       = "CONTROL_EVENT_RETENTION_DAYS"
	SettingAuditRetentionDays       = "CONTROL_AUDIT_RETENTION_DAYS"
	SettingOverviewScalePercent     = "CONTROL_OVERVIEW_SCALE_PERCENT"
	SettingRiskLoginWindow          = "CONTROL_RISK_LOGIN_WINDOW"
	SettingRiskLoginCount           = "CONTROL_RISK_LOGIN_COUNT"
	SettingRiskDeviceCount          = "CONTROL_RISK_DEVICE_COUNT"
	SettingDownloadMinContributions = "CONTROL_DOWNLOAD_MIN_CONTRIBUTIONS"
	SettingCOSRegion                = "CONTROL_COS_REGION"
	SettingCOSBucket                = "CONTROL_COS_BUCKET"
	SettingCOSSecretID              = "CONTROL_COS_SECRET_ID"
	SettingCOSSecretKey             = "CONTROL_COS_SECRET_KEY"
	SettingCOSWebsiteBucket         = "CONTROL_COS_WEBSITE_BUCKET"
	SettingCDNDomain                = "CONTROL_CDN_DOMAIN"
	SettingCDNAuthKey               = "CONTROL_CDN_AUTH_KEY"
	SettingCDNURLTTL                = "CONTROL_CDN_URL_TTL"
	SettingMonitorSecretID          = "CONTROL_MONITOR_SECRET_ID"
	SettingMonitorSecretKey         = "CONTROL_MONITOR_SECRET_KEY"
	SettingAIAPIKey                 = "CONTROL_AI_API_KEY"
	SettingAIBaseURL                = "CONTROL_AI_BASE_URL"
	SettingAIModel                  = "CONTROL_AI_MODEL"
	SettingAIWindows                = "CONTROL_AI_WINDOWS"
	SettingAITimezone               = "CONTROL_AI_TIMEZONE"
	SettingAIRequestTimeout         = "CONTROL_AI_REQUEST_TIMEOUT"
	SettingAIMaxAttempts            = "CONTROL_AI_MAX_ATTEMPTS"
	SettingAIImageQuality           = "CONTROL_AI_IMAGE_QUALITY"
	SettingAIImageMaxEdge           = "CONTROL_AI_IMAGE_MAX_EDGE"
	SettingAIPrecheckSystem         = "CONTROL_AI_PROMPT_PRECHECK_SYSTEM"
	SettingAIAnalysisSystem         = "CONTROL_AI_PROMPT_MERGED_ANALYSIS_SYSTEM"
	SettingAIPrecheckInstruction    = "CONTROL_AI_PROMPT_PRECHECK"
	SettingAIAnalysisInstruction    = "CONTROL_AI_PROMPT_ANALYSIS_INSTRUCTION"

	// Ingest switches: each client-side report can be accepted or ignored
	// without deploying a new build.
	SettingAcceptCourses     = "CONTROL_ACCEPT_COURSES"
	SettingAcceptTextbooks   = "CONTROL_ACCEPT_TEXTBOOKS"
	SettingAcceptSubmissions = "CONTROL_ACCEPT_SUBMISSIONS"
	SettingAcceptUserInfo    = "CONTROL_ACCEPT_USER_INFO"
	SettingAcceptEvents      = "CONTROL_ACCEPT_EVENTS"
)

// Setting kinds understood by the console form.
const (
	SettingKindString   = "string"
	SettingKindText     = "text"
	SettingKindInt      = "int"
	SettingKindBool     = "bool"
	SettingKindDuration = "duration"
	SettingKindURL      = "url"
	SettingKindSecret   = "secret"
)

// SettingSpec describes one runtime setting. Group orders the console form.
type SettingSpec struct {
	Key   string
	Group string
	Label string
	Kind  string
	Hint  string
	Min   int
	Max   int
}

// SettingGroups lists the console form sections in display order.
var SettingGroups = []struct {
	Key   string
	Label string
}{
	{"general", "常规"},
	{"risk", "风控"},
	{"storage", "对象存储"},
	{"cdn", "CDN"},
	{"monitoring", "主机监控"},
	{"ai", "AI 试卷解析"},
	{"ingest", "接收开关"},
}

// SettingSpecs is the single source of truth for console-editable settings.
// Every entry is stored in the app_settings table and applied without restart.
var SettingSpecs = []SettingSpec{
	{Key: SettingPublicBaseURL, Group: "general", Label: "对外访问地址", Kind: SettingKindURL, Hint: "例如 https://con.xuda.live"},

	{Key: SettingEventRetentionDays, Group: "general", Label: "事件保留天数", Kind: SettingKindInt, Min: 1, Max: 3650},
	{Key: SettingAuditRetentionDays, Group: "general", Label: "审计保留天数", Kind: SettingKindInt, Min: 1, Max: 3650},
	{Key: SettingOverviewScalePercent, Group: "general", Label: "概览显示比例", Kind: SettingKindInt, Hint: "100 = 真实值", Min: 0, Max: 100000},

	{Key: SettingRiskLoginWindow, Group: "risk", Label: "登录观察窗口", Kind: SettingKindDuration, Hint: "例如 10m"},
	{Key: SettingRiskLoginCount, Group: "risk", Label: "登录次数阈值", Kind: SettingKindInt, Min: 1, Max: 1000},
	{Key: SettingRiskDeviceCount, Group: "risk", Label: "设备数量阈值", Kind: SettingKindInt, Min: 1, Max: 1000},

	{Key: SettingDownloadMinContributions, Group: "general", Label: "下载所需贡献资源数", Kind: SettingKindInt, Hint: "0 = 不限制；按已发布的教材和试卷贡献数计算", Min: 0, Max: 100000},

	{Key: SettingCOSRegion, Group: "storage", Label: "地域", Kind: SettingKindString, Hint: "例如 ap-shanghai"},
	{Key: SettingCOSBucket, Group: "storage", Label: "资源桶", Kind: SettingKindString},
	{Key: SettingCOSSecretID, Group: "storage", Label: "SecretId", Kind: SettingKindSecret},
	{Key: SettingCOSSecretKey, Group: "storage", Label: "SecretKey", Kind: SettingKindSecret},
	{Key: SettingCOSWebsiteBucket, Group: "storage", Label: "网站桶", Kind: SettingKindString},

	{Key: SettingCDNDomain, Group: "cdn", Label: "CDN 域名", Kind: SettingKindURL, Hint: "例如 https://dl.xuda.live"},
	{Key: SettingCDNAuthKey, Group: "cdn", Label: "时间戳鉴权密钥", Kind: SettingKindSecret},
	{Key: SettingCDNURLTTL, Group: "cdn", Label: "签名有效期", Kind: SettingKindDuration, Hint: "例如 30m"},

	{Key: SettingMonitorSecretID, Group: "monitoring", Label: "SecretId", Kind: SettingKindSecret},
	{Key: SettingMonitorSecretKey, Group: "monitoring", Label: "SecretKey", Kind: SettingKindSecret},

	{Key: SettingAIAPIKey, Group: "ai", Label: "API Key", Kind: SettingKindSecret},
	{Key: SettingAIBaseURL, Group: "ai", Label: "API 地址", Kind: SettingKindURL},
	{Key: SettingAIModel, Group: "ai", Label: "模型", Kind: SettingKindString},
	{Key: SettingAIWindows, Group: "ai", Label: "解析时间窗", Kind: SettingKindString, Hint: "例如 00:00-08:50,12:00-13:50"},
	{Key: SettingAITimezone, Group: "ai", Label: "时区", Kind: SettingKindString},
	{Key: SettingAIRequestTimeout, Group: "ai", Label: "请求超时", Kind: SettingKindDuration},
	{Key: SettingAIMaxAttempts, Group: "ai", Label: "最大重试次数", Kind: SettingKindInt, Min: 1, Max: 10},
	{Key: SettingAIImageQuality, Group: "ai", Label: "图片质量", Kind: SettingKindInt, Min: 10, Max: 100},
	{Key: SettingAIImageMaxEdge, Group: "ai", Label: "图片最长边", Kind: SettingKindInt, Min: 320, Max: 4096},

	{Key: SettingAIPrecheckSystem, Group: "ai", Label: "预检系统提示词", Kind: SettingKindText, Hint: "留空则用内置模板"},
	{Key: SettingAIPrecheckInstruction, Group: "ai", Label: "预检指令", Kind: SettingKindText, Hint: "占位符依次为：试卷标题、检查页数、总页数（%s %d %d）"},
	{Key: SettingAIAnalysisSystem, Group: "ai", Label: "解析系统提示词", Kind: SettingKindText, Hint: "留空则用内置合并模板；模型需用 <!-- BASIC --> 和 <!-- MORE --> 分段"},
	{Key: SettingAIAnalysisInstruction, Group: "ai", Label: "解析指令", Kind: SettingKindText, Hint: "占位符依次为：试卷标题、总页数（%s %d）"},

	{Key: SettingAcceptCourses, Group: "ingest", Label: "接收课程上报", Kind: SettingKindBool, Hint: "关闭后 App 上报的课程不再新增或改名"},
	{Key: SettingAcceptTextbooks, Group: "ingest", Label: "接收书单上报", Kind: SettingKindBool, Hint: "关闭后 App 上报的教材名称不再写入书单"},
	{Key: SettingAcceptSubmissions, Group: "ingest", Label: "接收资源上传", Kind: SettingKindBool, Hint: "关闭后 App 无法创建上传票据（审核区不再有新提交）"},
	{Key: SettingAcceptUserInfo, Group: "ingest", Label: "接收用户信息更新", Kind: SettingKindBool, Hint: "关闭后登录时上报的昵称/学院/班级/专业不再更新"},
	{Key: SettingAcceptEvents, Group: "ingest", Label: "接收事件上报", Kind: SettingKindBool, Hint: "关闭后事件上报直接丢弃，客户端不会重试"},
}

// SettingSpecByKey returns the spec for a setting key.
func SettingSpecByKey(key string) (SettingSpec, bool) {
	for _, spec := range SettingSpecs {
		if spec.Key == key {
			return spec, true
		}
	}
	return SettingSpec{}, false
}

// SettingPermission keeps shared controls under the same grant on every form.
func SettingPermission(key string) string {
	if key == SettingOverviewScalePercent {
		return "overview"
	}
	spec, ok := SettingSpecByKey(key)
	if !ok {
		return ""
	}
	return "settings-" + spec.Group
}

// RuntimeValues renders the current runtime settings as stored values. Secrets
// are included in plaintext here; the application encrypts them before saving
// and never exposes them through the API.
func RuntimeValues(cfg Config) map[string]string {
	return map[string]string{
		SettingPublicBaseURL:            cfg.PublicBaseURL,
		SettingEventRetentionDays:       strconv.Itoa(cfg.EventRetentionDays),
		SettingAuditRetentionDays:       strconv.Itoa(cfg.AuditRetentionDays),
		SettingOverviewScalePercent:     strconv.Itoa(cfg.OverviewScalePercent),
		SettingRiskLoginWindow:          cfg.RiskLoginWindow.String(),
		SettingRiskLoginCount:           strconv.Itoa(cfg.RiskLoginCount),
		SettingRiskDeviceCount:          strconv.Itoa(cfg.RiskDeviceCount),
		SettingDownloadMinContributions: strconv.Itoa(cfg.DownloadMinContributions),
		SettingCOSRegion:                cfg.COSRegion,
		SettingCOSBucket:                cfg.COSBucket,
		SettingCOSSecretID:              cfg.COSSecretID,
		SettingCOSSecretKey:             cfg.COSSecretKey,
		SettingCOSWebsiteBucket:         cfg.COSWebsiteBucket,
		SettingCDNDomain:                cfg.CDNDomain,
		SettingCDNAuthKey:               cfg.CDNAuthKey,
		SettingCDNURLTTL:                cfg.CDNURLTTL.String(),
		SettingMonitorSecretID:          cfg.MonitorSecretID,
		SettingMonitorSecretKey:         cfg.MonitorSecretKey,
		SettingAIAPIKey:                 cfg.AIAPIKey,
		SettingAIBaseURL:                cfg.AIBaseURL,
		SettingAIModel:                  cfg.AIModel,
		SettingAIWindows:                cfg.AIWindows,
		SettingAITimezone:               cfg.AITimezone,
		SettingAIRequestTimeout:         cfg.AIRequestTimeout.String(),
		SettingAIMaxAttempts:            strconv.Itoa(cfg.AIMaxAttempts),
		SettingAIImageQuality:           strconv.Itoa(cfg.AIImageQuality),
		SettingAIImageMaxEdge:           strconv.Itoa(cfg.AIImageMaxEdge),
		SettingAIPrecheckSystem:         cfg.AIPrecheckSystem,
		SettingAIAnalysisSystem:         cfg.AIAnalysisSystem,
		SettingAIPrecheckInstruction:    cfg.AIPrecheckInstruction,
		SettingAIAnalysisInstruction:    cfg.AIAnalysisInstruction,
		SettingAcceptCourses:            strconv.FormatBool(cfg.IngestCourses.Enabled()),
		SettingAcceptTextbooks:          strconv.FormatBool(cfg.IngestTextbooks.Enabled()),
		SettingAcceptSubmissions:        strconv.FormatBool(cfg.IngestSubmissions.Enabled()),
		SettingAcceptUserInfo:           strconv.FormatBool(cfg.IngestUserInfo.Enabled()),
		SettingAcceptEvents:             strconv.FormatBool(cfg.IngestEvents.Enabled()),
	}
}

// ApplyRuntime validates and applies stored settings on top of cfg. Values not
// present in the map keep the value already in cfg, so a partially seeded
// database still starts.
func parseDurationValue(raw string) (time.Duration, error) {
	return time.ParseDuration(strings.TrimSpace(raw))
}

func ApplyRuntime(cfg *Config, values map[string]string) error {
	get := func(key string) (string, bool) {
		value, ok := values[key]
		if !ok {
			return "", false
		}
		return strings.TrimSpace(value), true
	}
	parseInt := func(key string, min, max int, target *int) error {
		raw, ok := get(key)
		if !ok {
			return nil
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("%s 需要是整数", key)
		}
		if value < min || value > max {
			return fmt.Errorf("%s 需要在 %d 到 %d 之间", key, min, max)
		}
		*target = value
		return nil
	}
	parseDuration := func(key string, target *time.Duration) error {
		raw, ok := get(key)
		if !ok {
			return nil
		}
		value, err := parseDurationValue(raw)
		if err != nil || value <= 0 {
			return fmt.Errorf("%s 需要是大于 0 的时长，例如 10m", key)
		}
		*target = value
		return nil
	}

	if raw, ok := get(SettingPublicBaseURL); ok {
		cfg.PublicBaseURL = strings.TrimRight(raw, "/")
	}
	if err := parseInt(SettingEventRetentionDays, 1, 3650, &cfg.EventRetentionDays); err != nil {
		return err
	}
	if err := parseInt(SettingAuditRetentionDays, 1, 3650, &cfg.AuditRetentionDays); err != nil {
		return err
	}
	if err := parseInt(SettingOverviewScalePercent, 0, 100000, &cfg.OverviewScalePercent); err != nil {
		return err
	}
	if err := parseDuration(SettingRiskLoginWindow, &cfg.RiskLoginWindow); err != nil {
		return err
	}
	if err := parseInt(SettingRiskLoginCount, 1, 1000, &cfg.RiskLoginCount); err != nil {
		return err
	}
	if err := parseInt(SettingRiskDeviceCount, 1, 1000, &cfg.RiskDeviceCount); err != nil {
		return err
	}
	if err := parseInt(SettingDownloadMinContributions, 0, 100000, &cfg.DownloadMinContributions); err != nil {
		return err
	}

	for key, target := range map[string]*string{
		SettingAIPrecheckSystem:      &cfg.AIPrecheckSystem,
		SettingAIAnalysisSystem:      &cfg.AIAnalysisSystem,
		SettingAIPrecheckInstruction: &cfg.AIPrecheckInstruction,
		SettingAIAnalysisInstruction: &cfg.AIAnalysisInstruction,
		SettingCOSRegion:             &cfg.COSRegion,
		SettingCOSBucket:             &cfg.COSBucket,
		SettingCOSSecretID:           &cfg.COSSecretID,
		SettingCOSSecretKey:          &cfg.COSSecretKey,
		SettingCOSWebsiteBucket:      &cfg.COSWebsiteBucket,
		SettingCDNAuthKey:            &cfg.CDNAuthKey,
		SettingMonitorSecretID:       &cfg.MonitorSecretID,
		SettingMonitorSecretKey:      &cfg.MonitorSecretKey,
		SettingAIAPIKey:              &cfg.AIAPIKey,
		SettingAIModel:               &cfg.AIModel,
		SettingAIWindows:             &cfg.AIWindows,
		SettingAITimezone:            &cfg.AITimezone,
	} {
		if raw, ok := get(key); ok {
			*target = raw
		}
	}
	for key, target := range map[string]*string{
		SettingCDNDomain: &cfg.CDNDomain,
		SettingAIBaseURL: &cfg.AIBaseURL,
	} {
		if raw, ok := get(key); ok {
			*target = strings.TrimRight(raw, "/")
		}
	}
	if err := parseDuration(SettingCDNURLTTL, &cfg.CDNURLTTL); err != nil {
		return err
	}
	if err := parseDuration(SettingAIRequestTimeout, &cfg.AIRequestTimeout); err != nil {
		return err
	}
	if err := parseInt(SettingAIMaxAttempts, 1, 10, &cfg.AIMaxAttempts); err != nil {
		return err
	}
	if err := parseInt(SettingAIImageQuality, 10, 100, &cfg.AIImageQuality); err != nil {
		return err
	}
	if err := parseInt(SettingAIImageMaxEdge, 320, 4096, &cfg.AIImageMaxEdge); err != nil {
		return err
	}
	// Blank prompts fall back to the built-in templates.
	for _, item := range []struct {
		value    *string
		fallback string
	}{
		{&cfg.AIPrecheckSystem, ai.DefaultPrecheckSystem},
		{&cfg.AIAnalysisSystem, ai.DefaultAnalysisSystem},
		{&cfg.AIPrecheckInstruction, ai.DefaultPrecheckInstruction},
		{&cfg.AIAnalysisInstruction, ai.DefaultAnalysisInstruction},
	} {
		if strings.TrimSpace(*item.value) == "" {
			*item.value = item.fallback
		}
	}
	for key, target := range map[string]*IngestSwitch{
		SettingAcceptCourses:     &cfg.IngestCourses,
		SettingAcceptTextbooks:   &cfg.IngestTextbooks,
		SettingAcceptSubmissions: &cfg.IngestSubmissions,
		SettingAcceptUserInfo:    &cfg.IngestUserInfo,
		SettingAcceptEvents:      &cfg.IngestEvents,
	} {
		raw, ok := get(key)
		if !ok {
			continue
		}
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s 需要是 true 或 false", key)
		}
		*target = IngestSwitch{Disabled: !value}
	}

	// No separate switch: AI runs whenever an API key is configured.
	cfg.AIAnalysisEnabled = strings.TrimSpace(cfg.AIAPIKey) != ""
	return nil
}
