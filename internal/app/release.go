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

const appReleaseAuditTarget = "current"

var appVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z._+-]{0,63}$`)

// AppReleaseInput is the small public contract used by the administrator
// console. Version values follow a conservative numeric version format; the
// URL must be an absolute HTTP(S) URL without embedded credentials.
type AppReleaseInput struct {
	LatestVersion string `json:"latestVersion"`
	DownloadURL   string `json:"downloadUrl"`
}

type AppReleaseView struct {
	LatestVersion string `json:"latestVersion"`
	DownloadURL   string `json:"downloadUrl"`
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
	return sqlite.AppReleaseConfig{LatestVersion: in.LatestVersion, DownloadURL: in.DownloadURL}, nil
}

func appReleaseView(config sqlite.AppReleaseConfig) AppReleaseView {
	view := AppReleaseView{LatestVersion: config.LatestVersion, DownloadURL: config.DownloadURL}
	if !config.UpdatedAt.IsZero() {
		view.UpdatedAt = config.UpdatedAt.Format(time.RFC3339)
	}
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
	if err := a.Store.UpdateAppReleaseConfig(ctx, config); err != nil {
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
