package app

import (
	"context"
	"time"
)

// RefreshStatsOnce warms the overview caches. It runs on startup and then every
// 10 minutes so the console normally reads a warm cache instead of waiting on
// the Tencent Cloud APIs.
func (a *App) RefreshStatsOnce(ctx context.Context) {
	refreshCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if _, err := a.CDNStats(refreshCtx, 30, true); err != nil {
		a.Logger.Warn("refresh cdn stats failed", "error", err)
	}
	if _, err := a.BucketStats(refreshCtx, 30, true); err != nil {
		a.Logger.Warn("refresh bucket stats failed", "error", err)
	}
	if _, err := a.HostStats(refreshCtx, 30, true); err != nil {
		a.Logger.Warn("refresh host stats failed", "error", err)
	}
}
