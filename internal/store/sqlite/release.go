package sqlite

import (
	"context"
	"time"
)

// AppReleaseConfig contains the public APP update metadata managed by the
// administrator. The row is a singleton and is always stored with id = 1.
type AppReleaseConfig struct {
	LatestVersion string
	DownloadURL   string
	UpdatedAt     time.Time
}

func (s *Store) GetAppReleaseConfig(ctx context.Context) (AppReleaseConfig, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT latest_version, download_url, updated_at FROM app_release_config WHERE id = 1")
	var config AppReleaseConfig
	var updatedAt int64
	if err := row.Scan(&config.LatestVersion, &config.DownloadURL, &updatedAt); err != nil {
		return AppReleaseConfig{}, err
	}
	if updatedAt > 0 {
		config.UpdatedAt = fromMillis(updatedAt)
	}
	return config, nil
}

func (s *Store) UpdateAppReleaseConfig(ctx context.Context, config AppReleaseConfig) (AppReleaseConfig, error) {
	var updatedAt int64
	err := s.DB.QueryRowContext(ctx,
		`UPDATE app_release_config SET latest_version = ?, download_url = ?, updated_at = MAX(updated_at + 1, ?)
		WHERE id = 1 RETURNING updated_at`,
		config.LatestVersion, config.DownloadURL, millis(config.UpdatedAt)).Scan(&updatedAt)
	if err != nil {
		return AppReleaseConfig{}, err
	}
	config.UpdatedAt = fromMillis(updatedAt)
	return config, nil
}
