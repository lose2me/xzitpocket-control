package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

const (
	appReleaseAuditTarget     = "current"
	appReleaseTestAuditTarget = "test"
	// appReleaseChangelogMaxBytes bounds the Markdown changelog payload.
	appReleaseChangelogMaxBytes = 64 << 10
)

var appVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z._+-]{0,63}$`)

// AppReleaseInput is the small public contract used by the administrator
// console. Version values follow a conservative numeric version format; the
// URL must be an absolute HTTP(S) URL without embedded credentials. The
// changelog is Markdown rendered by the APP with its shared renderer.
type AppReleaseInput struct {
	LatestVersion string `json:"latestVersion"`
	DownloadURL   string `json:"downloadUrl"`
	Changelog     string `json:"changelog"`
}

type AppReleaseView struct {
	LatestVersion string `json:"latestVersion"`
	DownloadURL   string `json:"downloadUrl"`
	Changelog     string `json:"changelog"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

func validateAppReleaseInput(in AppReleaseInput) (sqlite.AppReleaseConfig, error) {
	in.LatestVersion = strings.TrimSpace(in.LatestVersion)
	in.DownloadURL = strings.TrimSpace(in.DownloadURL)
	if in.LatestVersion == "" || !appVersionPattern.MatchString(in.LatestVersion) {
		return sqlite.AppReleaseConfig{}, Err("invalid_app_version", "版本号格式无效", http.StatusBadRequest)
	}
	if len(in.DownloadURL) > 2048 || strings.ContainsAny(in.DownloadURL, " \t\r\n\x00") {
		return sqlite.AppReleaseConfig{}, Err("invalid_download_url", "下载地址无效", http.StatusBadRequest)
	}
	parsed, err := url.Parse(in.DownloadURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return sqlite.AppReleaseConfig{}, Err("invalid_download_url", "下载地址必须是 HTTP 或 HTTPS 地址", http.StatusBadRequest)
	}
	if strings.ContainsRune(in.Changelog, 0) || len(in.Changelog) > appReleaseChangelogMaxBytes {
		return sqlite.AppReleaseConfig{}, Err("invalid_changelog", "更新日志过长或包含非法字符", http.StatusBadRequest)
	}
	return sqlite.AppReleaseConfig{LatestVersion: in.LatestVersion, DownloadURL: in.DownloadURL, Changelog: in.Changelog}, nil
}

func appReleaseView(config sqlite.AppReleaseConfig) AppReleaseView {
	view := AppReleaseView{LatestVersion: config.LatestVersion, DownloadURL: config.DownloadURL, Changelog: config.Changelog}
	view.UpdatedAt = configVersion(config.UpdatedAt)
	return view
}

func (a *App) GetAppReleaseConfig(ctx context.Context) (AppReleaseView, error) {
	config, err := a.Store.GetAppReleaseConfig(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return AppReleaseView{}, ErrNotFound
	}
	if err != nil {
		return AppReleaseView{}, err
	}
	return appReleaseView(config), nil
}

func (a *App) UpdateAppReleaseConfig(ctx context.Context, in AppReleaseInput, actor string) (AppReleaseView, error) {
	config, err := validateAppReleaseInput(in)
	if err != nil {
		return AppReleaseView{}, err
	}
	now := time.Now().UTC()
	config.UpdatedAt = now
	config, err = a.Store.UpdateAppReleaseConfig(ctx, config)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AppReleaseView{}, ErrNotFound
		}
		return AppReleaseView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "app_release_update", "app_release", appReleaseAuditTarget,
		controlcrypto.JSON(map[string]string{
			"latest_version": config.LatestVersion,
			"download_url":   config.DownloadURL,
		}), now)
	return appReleaseView(config), nil
}

// GetAppReleaseTestConfig serves the test release singleton. It never falls
// back to the production configuration: the two are fully independent.
func (a *App) GetAppReleaseTestConfig(ctx context.Context) (AppReleaseView, error) {
	config, err := a.Store.GetAppReleaseTestConfig(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return AppReleaseView{}, ErrNotFound
	}
	if err != nil {
		return AppReleaseView{}, err
	}
	return appReleaseView(config), nil
}

func (a *App) UpdateAppReleaseTestConfig(ctx context.Context, in AppReleaseInput, actor string) (AppReleaseView, error) {
	config, err := validateAppReleaseInput(in)
	if err != nil {
		return AppReleaseView{}, err
	}
	now := time.Now().UTC()
	config.UpdatedAt = now
	config, err = a.Store.UpdateAppReleaseTestConfig(ctx, config)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AppReleaseView{}, ErrNotFound
		}
		return AppReleaseView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "app_release_test_update", "app_release", appReleaseTestAuditTarget,
		controlcrypto.JSON(map[string]string{
			"latest_version": config.LatestVersion,
			"download_url":   config.DownloadURL,
		}), now)
	return appReleaseView(config), nil
}
