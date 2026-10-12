package sqlite

import (
	"context"
	"time"
)

// AppReleaseConfig contains the public APP update metadata managed by the
// administrator. The row is a singleton and is always stored with id = 1.
// The same shape backs the separate test release configuration.
type AppReleaseConfig struct {
	LatestVersion string
	DownloadURL   string
	Changelog     string
	UpdatedAt     time.Time
}

const (
	appReleaseTable     = "app_release_config"
	appReleaseTestTable = "app_release_test_config"
)

func (s *Store) GetAppReleaseConfig(ctx context.Context) (AppReleaseConfig, error) {
	return s.getAppReleaseConfig(ctx, appReleaseTable)
}

func (s *Store) UpdateAppReleaseConfig(ctx context.Context, config AppReleaseConfig) (AppReleaseConfig, error) {
	return s.updateAppReleaseConfig(ctx, appReleaseTable, config)
}

// GetAppReleaseTestConfig reads the test release singleton. It is fully
// independent from the production configuration.
func (s *Store) GetAppReleaseTestConfig(ctx context.Context) (AppReleaseConfig, error) {
	return s.getAppReleaseConfig(ctx, appReleaseTestTable)
}

// UpdateAppReleaseTestConfig writes the test release singleton.
func (s *Store) UpdateAppReleaseTestConfig(ctx context.Context, config AppReleaseConfig) (AppReleaseConfig, error) {
	return s.updateAppReleaseConfig(ctx, appReleaseTestTable, config)
}

func (s *Store) getAppReleaseConfig(ctx context.Context, table string) (AppReleaseConfig, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT latest_version, download_url, changelog, updated_at FROM "+table+" WHERE id = 1")
	var config AppReleaseConfig
	var updatedAt int64
	if err := row.Scan(&config.LatestVersion, &config.DownloadURL, &config.Changelog, &updatedAt); err != nil {
		return AppReleaseConfig{}, err
	}
	if updatedAt > 0 {
		config.UpdatedAt = fromMillis(updatedAt)
	}
	return config, nil
}

func (s *Store) updateAppReleaseConfig(ctx context.Context, table string, config AppReleaseConfig) (AppReleaseConfig, error) {
	var updatedAt int64
	err := s.DB.QueryRowContext(ctx,
		`UPDATE `+table+` SET latest_version = ?, download_url = ?, changelog = ?, updated_at = MAX(updated_at + 1, ?)
		WHERE id = 1 RETURNING updated_at`,
		config.LatestVersion, config.DownloadURL, config.Changelog, millis(config.UpdatedAt)).Scan(&updatedAt)
	if err != nil {
		return AppReleaseConfig{}, err
	}
	config.UpdatedAt = fromMillis(updatedAt)
	return config, nil
}
