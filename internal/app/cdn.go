package app

import (
	"context"
	"net/http"
	"time"

	"xzitpocket-control/internal/cloud"
)

// CDNStats returns the CDN metrics for the admin overview. Credentials are the
// same Tencent Cloud pair used for object storage and cache purging.
func (a *App) CDNStats(ctx context.Context, days int) (*cloud.CDNStats, error) {
	if a.Cfg.COSSecretID == "" || a.Cfg.COSSecretKey == "" {
		return nil, Err("cdn_unavailable", "CDN 未配置", http.StatusServiceUnavailable)
	}
	stats, err := cloud.FetchCDNStats(ctx, a.Cfg.COSSecretID, a.Cfg.COSSecretKey, days, time.Now())
	if err != nil {
		a.Logger.Warn("fetch cdn stats failed", "error", err)
		return nil, Err("cdn_stats_failed", "获取 CDN 统计失败", http.StatusBadGateway)
	}
	return stats, nil
}
