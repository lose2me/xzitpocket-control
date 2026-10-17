package app

import (
	"context"
	"net/http"
	"time"

	"xzitpocket-control/internal/cloud"
)

// cdnStatsCacheTTL keeps the overview fast: the CDN API needs several calls per
// refresh and its metrics only move on a minute scale.
const cdnStatsCacheTTL = 60 * time.Second

type cdnStatsCache struct {
	stats   *cloud.CDNStats
	days    int
	expires time.Time
}

// CDNStats returns the CDN metrics for the admin overview. Results are cached
// briefly (refresh bypasses the cache) so opening the overview does not wait on
// the CDN API every time.
func (a *App) CDNStats(ctx context.Context, days int, refresh bool) (*cloud.CDNStats, error) {
	if a.Cfg.COSSecretID == "" || a.Cfg.COSSecretKey == "" {
		return nil, Err("cdn_unavailable", "CDN 未配置", http.StatusServiceUnavailable)
	}
	now := time.Now()
	if !refresh {
		a.cdnMu.Lock()
		cached := a.cdnCache
		a.cdnMu.Unlock()
		if cached.stats != nil && cached.days == days && now.Before(cached.expires) {
			return cached.stats, nil
		}
	}
	stats, err := cloud.FetchCDNStats(ctx, a.Cfg.COSSecretID, a.Cfg.COSSecretKey, days, now)
	if err != nil {
		a.Logger.Warn("fetch cdn stats failed", "error", err)
		return nil, Err("cdn_stats_failed", "获取 CDN 统计失败", http.StatusBadGateway)
	}
	a.cdnMu.Lock()
	a.cdnCache = cdnStatsCache{stats: stats, days: stats.Days, expires: now.Add(cdnStatsCacheTTL)}
	a.cdnMu.Unlock()
	return stats, nil
}
