package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"xzitpocket-control/internal/cloud"
)

const (
	bucketStatsCacheTTL    = 10 * time.Minute
	bucketStatsRefreshTime = 30 * time.Second
)

type bucketStatsCache struct {
	stats      *cloud.BucketStatsResponse
	days       int
	expires    time.Time
	refreshing bool
}

type bucketTarget struct {
	key    string
	label  string
	bucket string
}

// bucketLabel drops the trailing "-<appid>" so tabs show a readable name.
func bucketLabel(bucket string) string {
	if index := strings.LastIndex(bucket, "-"); index > 0 {
		return bucket[:index]
	}
	return bucket
}

func (a *App) bucketTargets() []bucketTarget {
	targets := make([]bucketTarget, 0, 2)
	if a.Cfg.COSBucket != "" {
		targets = append(targets, bucketTarget{key: "assets", label: bucketLabel(a.Cfg.COSBucket), bucket: a.Cfg.COSBucket})
	}
	if a.Cfg.COSWebsiteBucket != "" {
		targets = append(targets, bucketTarget{key: "website", label: bucketLabel(a.Cfg.COSWebsiteBucket), bucket: a.Cfg.COSWebsiteBucket})
	}
	return targets
}

// BucketStats returns the two COS buckets' usage. Like the CDN stats it is
// cached for 10 minutes and revalidated in the background once stale.
func (a *App) BucketStats(ctx context.Context, days int, refresh bool) (*cloud.BucketStatsResponse, error) {
	if a.Cfg.COSSecretID == "" || a.Cfg.COSSecretKey == "" {
		return nil, Err("bucket_unavailable", "对象存储未配置", http.StatusServiceUnavailable)
	}
	if len(a.bucketTargets()) == 0 {
		return nil, Err("bucket_unavailable", "对象存储未配置", http.StatusServiceUnavailable)
	}
	now := time.Now()
	if !refresh {
		a.bucketMu.Lock()
		cached := a.bucketCache
		a.bucketMu.Unlock()
		if cached.stats != nil && cached.days == days {
			if now.Before(cached.expires) {
				return cached.stats, nil
			}
			a.startBucketRefresh(days)
			stale := *cached.stats
			stale.Stale = true
			return &stale, nil
		}
	}
	return a.fetchBucketStats(ctx, days, now)
}

func (a *App) fetchBucketStats(ctx context.Context, days int, now time.Time) (*cloud.BucketStatsResponse, error) {
	result := &cloud.BucketStatsResponse{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Days:        days,
		Buckets:     make([]cloud.BucketStats, 0, 2),
	}
	for _, target := range a.bucketTargets() {
		item, err := cloud.FetchBucketStats(ctx, a.Cfg.COSSecretID, a.Cfg.COSSecretKey, a.Cfg.COSRegion, target.bucket, days, now)
		if err != nil {
			a.Logger.Warn("fetch bucket stats failed", "bucket", target.bucket, "error", err)
			continue
		}
		item.Key = target.key
		item.Label = target.label
		result.Buckets = append(result.Buckets, item)
	}
	if len(result.Buckets) == 0 {
		return nil, Err("bucket_stats_failed", "获取存储桶统计失败", http.StatusBadGateway)
	}
	a.bucketMu.Lock()
	a.bucketCache = bucketStatsCache{stats: result, days: days, expires: now.Add(bucketStatsCacheTTL)}
	a.bucketMu.Unlock()
	return result, nil
}

func (a *App) startBucketRefresh(days int) {
	a.bucketMu.Lock()
	if a.bucketCache.refreshing {
		a.bucketMu.Unlock()
		return
	}
	a.bucketCache.refreshing = true
	a.bucketMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bucketStatsRefreshTime)
		defer cancel()
		_, _ = a.fetchBucketStats(ctx, days, time.Now())
		a.bucketMu.Lock()
		a.bucketCache.refreshing = false
		a.bucketMu.Unlock()
	}()
}
