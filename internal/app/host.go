package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"xzitpocket-control/internal/cloud"
)

const hostStatsCacheTTL = 10 * time.Minute

type hostStatsCache struct {
	stats   *cloud.HostStats
	expires time.Time
}

func metadataValue(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://metadata.tencentyun.com"+path, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return strings.TrimSpace(string(raw)), nil
}

// HostStats returns the local Lighthouse machine's monitoring data. The
// instance is looked up by the metadata public IP, so no instance id has to be
// configured.
func (a *App) HostStats(ctx context.Context, days int, refresh bool) (*cloud.HostStats, error) {
	if a.cfg().MonitorSecretID == "" || a.cfg().MonitorSecretKey == "" {
		return nil, Err("host_unavailable", "主机监控未配置", http.StatusServiceUnavailable)
	}
	now := time.Now()
	if !refresh {
		a.hostMu.Lock()
		cached := a.hostCache
		a.hostMu.Unlock()
		if cached.stats != nil && now.Before(cached.expires) {
			return cached.stats, nil
		}
	}
	region, err := metadataValue(ctx, "/latest/meta-data/placement/region")
	if err != nil || region == "" {
		a.Logger.Warn("read instance region failed", "error", err)
		return nil, Err("host_unavailable", "无法获取主机信息", http.StatusServiceUnavailable)
	}
	publicIP, err := metadataValue(ctx, "/latest/meta-data/public-ipv4")
	if err != nil || publicIP == "" {
		a.Logger.Warn("read instance public ip failed", "error", err)
		return nil, Err("host_unavailable", "无法获取主机信息", http.StatusServiceUnavailable)
	}
	stats, err := cloud.FetchHostStats(ctx, a.cfg().MonitorSecretID, a.cfg().MonitorSecretKey, region, publicIP, days, now)
	if err != nil {
		a.Logger.Warn("fetch host stats failed", "error", err)
		return nil, Err("host_stats_failed", "获取主机统计失败", http.StatusBadGateway)
	}
	a.hostMu.Lock()
	a.hostCache = hostStatsCache{stats: stats, expires: now.Add(hostStatsCacheTTL)}
	a.hostMu.Unlock()
	return stats, nil
}
