package app

import (
	"context"
	"net/http"
	"time"

	"xzitpocket-control/internal/cloud"
)

const (
	// cdnStatsCacheTTL is how long a fetched result counts as fresh. After it
	// expires the cached result is still served and refreshed in the background.
	cdnStatsCacheTTL = 10 * time.Minute
	// cdnStatsRefreshTimeout bounds a background revalidation.
	cdnStatsRefreshTimeout = 30 * time.Second
)

type cdnStatsCache struct {
	stats      *cloud.CDNStats
	days       int
	expires    time.Time
	refreshing bool
}

// CDNStats returns the CDN metrics for the admin overview. A warm cache is
// served immediately; when it is older than the TTL the stale result is
// returned while a background goroutine revalidates it. refresh bypasses the
// cache and waits for fresh data.
func (a *App) CDNStats(ctx context.Context, days int, refresh bool) (*cloud.CDNStats, error) {
	if a.cfg().COSSecretID == "" || a.cfg().COSSecretKey == "" {
		return nil, Err("cdn_unavailable", "CDN 未配置", http.StatusServiceUnavailable)
	}
	now := time.Now()

	a.cdnMu.Lock()
	cached := a.cdnCache
	a.cdnMu.Unlock()

	if !refresh && cached.stats != nil && cached.days == days {
		if now.Before(cached.expires) {
			return cached.stats, nil
		}
		// Stale but usable: answer now, revalidate silently in the background.
		a.startCDNRefresh(days)
		stale := *cached.stats
		stale.Stale = true
		return &stale, nil
	}

	return a.fetchCDNStats(ctx, days, now)
}

func (a *App) fetchCDNStats(ctx context.Context, days int, now time.Time) (*cloud.CDNStats, error) {
	stats, err := cloud.FetchCDNStats(ctx, a.cfg().COSSecretID, a.cfg().COSSecretKey, days, now)
	if err != nil {
		a.Logger.Warn("fetch cdn stats failed", "error", err)
		return nil, Err("cdn_stats_failed", "获取 CDN 统计失败", http.StatusBadGateway)
	}
	a.cdnMu.Lock()
	a.cdnCache = cdnStatsCache{stats: stats, days: stats.Days, expires: now.Add(cdnStatsCacheTTL)}
	a.cdnMu.Unlock()
	return stats, nil
}

// startCDNRefresh revalidates the cache without blocking the caller. The
// request context must not be used because it is cancelled once the handler
// returns.
func (a *App) startCDNRefresh(days int) {
	a.cdnMu.Lock()
	if a.cdnCache.refreshing {
		a.cdnMu.Unlock()
		return
	}
	a.cdnCache.refreshing = true
	a.cdnMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cdnStatsRefreshTimeout)
		defer cancel()
		_, _ = a.fetchCDNStats(ctx, days, time.Now())
		a.cdnMu.Lock()
		a.cdnCache.refreshing = false
		a.cdnMu.Unlock()
	}()
}
