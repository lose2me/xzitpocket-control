package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BucketStatPoint is one day of a bucket metric series.
type BucketStatPoint struct {
	Day   string  `json:"day"`
	Value float64 `json:"value"`
}

// BucketStats is one COS bucket's usage for the overview.
type BucketStats struct {
	Key              string           `json:"key"`
	Label            string           `json:"label"`
	Bucket           string           `json:"bucket"`
	StdStorageMB     float64          `json:"std_storage_mb"`
	ObjectNumber     float64          `json:"object_number"`
	TotalRequests    float64          `json:"total_requests"`
	CdnOriginTraffic float64          `json:"cdn_origin_traffic"`
	InboundTraffic   float64          `json:"inbound_traffic"`
	InternetTraffic  float64          `json:"internet_traffic"`
	Status2xx        float64          `json:"status_2xx"`
	Status4xx        float64          `json:"status_4xx"`
	Status5xx        float64          `json:"status_5xx"`
	OriginSeries     []BucketStatPoint `json:"origin_series"`
}

// BucketStatsResponse is the payload for the two bucket tabs.
type BucketStatsResponse struct {
	GeneratedAt string        `json:"generated_at"`
	Days        int           `json:"days"`
	Stale       bool          `json:"stale,omitempty"`
	Buckets     []BucketStats `json:"buckets"`
}

func bucketAppID(bucket string) string {
	if index := strings.LastIndex(bucket, "-"); index >= 0 {
		return bucket[index+1:]
	}
	return ""
}

func cosMetric(ctx context.Context, secretID, secretKey, region, bucket, metric, start, end string) ([]int64, []float64, error) {
	raw, err := txCall(ctx, secretID, secretKey, "monitor", "monitor.tencentcloudapi.com",
		"GetMonitorData", "2018-07-24", region, map[string]any{
			"Namespace":  "QCE/COS",
			"MetricName": metric,
			"Instances": []any{map[string]any{"Dimensions": []any{
				map[string]any{"Name": "appid", "Value": bucketAppID(bucket)},
				map[string]any{"Name": "bucket", "Value": bucket},
				map[string]any{"Name": "region", "Value": region},
			}}},
			"Period":    3600,
			"StartTime": start,
			"EndTime":   end,
		})
	if err != nil {
		return nil, nil, err
	}
	var resp monitorResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, err
	}
	if len(resp.Response.DataPoints) == 0 {
		return nil, nil, nil
	}
	point := resp.Response.DataPoints[0]
	return point.Timestamps, point.Values, nil
}

func sumValues(values []float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total
}

func dailySum(timestamps []int64, values []float64, loc *time.Location) []BucketStatPoint {
	byDay := make(map[string]float64)
	for i, ts := range timestamps {
		if i >= len(values) {
			break
		}
		day := time.Unix(ts, 0).In(loc).Format("2006-01-02")
		byDay[day] += values[i]
	}
	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)
	series := make([]BucketStatPoint, 0, len(days))
	for _, day := range days {
		series = append(series, BucketStatPoint{Day: day, Value: byDay[day]})
	}
	return series
}

// FetchBucketStats reads one COS bucket's usage from Tencent Cloud monitoring.
func FetchBucketStats(ctx context.Context, secretID, secretKey, region, bucket string, days int, now time.Time) (BucketStats, error) {
	stats := BucketStats{Bucket: bucket}
	if secretID == "" || secretKey == "" {
		return stats, fmt.Errorf("cos credentials are not configured")
	}
	if days < 1 {
		days = 30
	}
	if days > 30 {
		days = 30
	}
	loc := time.FixedZone("CST", 8*3600)
	end := now.In(loc).Format("2006-01-02T15:04:05+08:00")
	start := now.In(loc).AddDate(0, 0, -days).Format("2006-01-02T15:04:05+08:00")

	type metricPlan struct {
		name string
		key  string
		last bool
	}
	plans := []metricPlan{
		{"StdStorage", "storage", true},
		{"StdObjectNumber", "objects", true},
		{"TotalRequests", "requests", false},
		{"CdnOriginTraffic", "origin", false},
		{"InboundTraffic", "inbound", false},
		{"InternetTraffic", "internet", false},
		{"2xxResponse", "s2", false},
		{"4xxResponse", "s4", false},
		{"5xxResponse", "s5", false},
	}
	results := make(map[string]metricSeries, len(plans))
	for _, plan := range plans {
		timestamps, values, err := cosMetric(ctx, secretID, secretKey, region, bucket, plan.name, start, end)
		if err != nil {
			return stats, err
		}
		results[plan.key] = metricSeries{timestamps: timestamps, values: values}
	}
	stats.StdStorageMB = lastValue(results["storage"].values)
	stats.ObjectNumber = lastValue(results["objects"].values)
	stats.TotalRequests = sumValues(results["requests"].values)
	stats.CdnOriginTraffic = sumValues(results["origin"].values)
	stats.InboundTraffic = sumValues(results["inbound"].values)
	stats.InternetTraffic = sumValues(results["internet"].values)
	stats.Status2xx = sumValues(results["s2"].values)
	stats.Status4xx = sumValues(results["s4"].values)
	stats.Status5xx = sumValues(results["s5"].values)
	stats.OriginSeries = dailySum(results["origin"].timestamps, results["origin"].values, loc)
	return stats, nil
}
