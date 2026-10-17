package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CDNStatPoint is one day of a CDN metric series.
type CDNStatPoint struct {
	Day   string `json:"day"`
	Value int64  `json:"value"`
}

// CDNDomainStats is the statistics of one CDN domain.
type CDNDomainStats struct {
	Domain          string         `json:"domain"`
	TodayFlux       int64          `json:"today_flux"`
	TodayRequests   int64          `json:"today_requests"`
	TodayOriginFlux int64          `json:"today_origin_flux"`
	Flux            int64          `json:"flux"`
	Requests        int64          `json:"requests"`
	OriginFlux      int64          `json:"origin_flux"`
	BandwidthPeak   int64          `json:"bandwidth_peak"`
	HitRate         float64        `json:"hit_rate"`
	FluxSeries      []CDNStatPoint `json:"flux_series"`
	RequestSeries   []CDNStatPoint `json:"request_series"`
}

// CDNStatusCounts aggregates response status codes. The CDN API only exposes
// them across all domains, so a per-domain split is not available.
type CDNStatusCounts struct {
	Status2xx int64 `json:"status_2xx"`
	Status3xx int64 `json:"status_3xx"`
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
}

// CDNTrafficPackage summarises the enabled traffic packages.
type CDNTrafficPackage struct {
	Type       string `json:"type"`
	Count      int    `json:"count"`
	Total      int64  `json:"total"`
	Used       int64  `json:"used"`
	Remaining  int64  `json:"remaining"`
	ExpireTime string `json:"expire_time"`
}

// CDNStats is the payload rendered by the admin overview.
type CDNStats struct {
	GeneratedAt    string             `json:"generated_at"`
	Days           int                `json:"days"`
	Stale          bool               `json:"stale,omitempty"`
	PayType        string             `json:"pay_type"`
	TrafficPackage *CDNTrafficPackage `json:"traffic_package,omitempty"`
	Status         CDNStatusCounts    `json:"status"`
	Domains        []CDNDomainStats   `json:"domains"`
}

type cdnAPIError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

type cdnMetricPoint struct {
	Time  string `json:"Time"`
	Value int64  `json:"Value"`
}

type cdnMetric struct {
	Metric          string           `json:"Metric"`
	DetailData      []cdnMetricPoint `json:"DetailData"`
	SummarizedData  cdnMetricSummary   `json:"SummarizedData"`
	SummarizedDatas []cdnMetricSummary `json:"SummarizedDatas"`
}

type cdnMetricSummary struct {
	Name  string `json:"Name"`
	Value int64  `json:"Value"`
}

type cdnSeriesResponse struct {
	Response struct {
		Error *cdnAPIError `json:"Error"`
		Data  []struct {
			Resource   string      `json:"Resource"`
			CdnData    []cdnMetric `json:"CdnData"`
			OriginData []cdnMetric `json:"OriginData"`
		} `json:"Data"`
	} `json:"Response"`
}

func cdnCall(ctx context.Context, secretID, secretKey, action string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	timestamp := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+cdnAPIHost+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", tc3Authorization(secretID, secretKey, "cdn", cdnAPIHost, action, string(body), timestamp))
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", "2018-06-06")
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))

	resp, err := cdnAPIClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cdn %s: %s: %s", action, resp.Status, strings.TrimSpace(string(raw)))
	}
	var envelope struct {
		Response struct {
			Error *cdnAPIError `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("cdn %s: decode response: %w", action, err)
	}
	if envelope.Response.Error != nil {
		return nil, fmt.Errorf("cdn %s: %s: %s", action, envelope.Response.Error.Code, envelope.Response.Error.Message)
	}
	return raw, nil
}

func cdnFindMetric(metrics []cdnMetric, name string) *cdnMetric {
	for i := range metrics {
		if metrics[i].Metric == name {
			return &metrics[i]
		}
	}
	return nil
}

func cdnDomainNames(ctx context.Context, secretID, secretKey string) ([]string, error) {
	raw, err := cdnCall(ctx, secretID, secretKey, "DescribeDomains", map[string]any{"Limit": 100})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Response struct {
			Domains []struct {
				Domain string `json:"Domain"`
			} `json:"Domains"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Response.Domains))
	for _, item := range resp.Response.Domains {
		if item.Domain != "" {
			names = append(names, item.Domain)
		}
	}
	// The "dl." domain is the default tab, so keep it first.
	sort.SliceStable(names, func(i, j int) bool {
		left := strings.HasPrefix(names[i], "dl.")
		right := strings.HasPrefix(names[j], "dl.")
		if left != right {
			return left
		}
		return names[i] < names[j]
	})
	return names, nil
}

func cdnSeries(ctx context.Context, secretID, secretKey, action, metric string, domains []string, start, end string) (map[string]cdnMetric, error) {
	raw, err := cdnCall(ctx, secretID, secretKey, action, map[string]any{
		"StartTime": start, "EndTime": end, "Metric": metric, "Domains": domains, "Interval": "day", "Detail": true,
	})
	if err != nil {
		return nil, err
	}
	var resp cdnSeriesResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	result := make(map[string]cdnMetric, len(resp.Response.Data))
	for _, entry := range resp.Response.Data {
		metrics := entry.CdnData
		if action == "DescribeOriginData" {
			metrics = entry.OriginData
		}
		if found := cdnFindMetric(metrics, metric); found != nil {
			result[entry.Resource] = *found
		}
	}
	return result, nil
}

func cdnStatusCounts(ctx context.Context, secretID, secretKey, start, end string) (CDNStatusCounts, error) {
	var counts CDNStatusCounts
	for _, metric := range []string{"2xx", "3xx", "4xx", "5xx"} {
		raw, err := cdnCall(ctx, secretID, secretKey, "DescribeCdnData", map[string]any{
			"StartTime": start, "EndTime": end, "Metric": metric, "Interval": "day",
		})
		if err != nil {
			return counts, err
		}
		var resp cdnSeriesResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return counts, err
		}
		var value int64
		for _, entry := range resp.Response.Data {
			if found := cdnFindMetric(entry.CdnData, metric); found != nil {
				value += found.SummarizedData.Value
			}
		}
		switch metric {
		case "2xx":
			counts.Status2xx = value
		case "3xx":
			counts.Status3xx = value
		case "4xx":
			counts.Status4xx = value
		case "5xx":
			counts.Status5xx = value
		}
	}
	return counts, nil
}

func cdnPayType(ctx context.Context, secretID, secretKey string) (string, error) {
	raw, err := cdnCall(ctx, secretID, secretKey, "DescribePayType", map[string]any{})
	if err != nil {
		return "", err
	}
	var resp struct {
		Response struct {
			PayType string `json:"PayType"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.Response.PayType, nil
}

func cdnTrafficPackage(ctx context.Context, secretID, secretKey string) (*CDNTrafficPackage, error) {
	raw, err := cdnCall(ctx, secretID, secretKey, "DescribeTrafficPackages", map[string]any{"Limit": 100})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Response struct {
			TrafficPackages []struct {
				Type           string `json:"Type"`
				Bytes          int64  `json:"Bytes"`
				BytesUsed      int64  `json:"BytesUsed"`
				Status         string `json:"Status"`
				TrueExpireTime string `json:"TrueExpireTime"`
				ExpireTime     string `json:"ExpireTime"`
			} `json:"TrafficPackages"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	pkg := &CDNTrafficPackage{}
	found := false
	for _, item := range resp.Response.TrafficPackages {
		if item.Status != "enabled" {
			continue
		}
		found = true
		pkg.Count++
		pkg.Total += item.Bytes
		pkg.Used += item.BytesUsed
		expire := item.TrueExpireTime
		if expire == "" {
			expire = item.ExpireTime
		}
		// Multiple packages can be active; the soonest expiry is the one that
		// matters, so show it as the package expiry.
		if expire != "" && (pkg.ExpireTime == "" || expire < pkg.ExpireTime) {
			pkg.ExpireTime = expire
		}
	}
	if !found {
		return nil, nil
	}
	if pkg.Remaining = pkg.Total - pkg.Used; pkg.Remaining < 0 {
		pkg.Remaining = 0
	}
	return pkg, nil
}

func cdnPoints(points []cdnMetricPoint) []CDNStatPoint {
	result := make([]CDNStatPoint, 0, len(points))
	for _, point := range points {
		day := point.Time
		if len(day) >= 10 {
			day = day[:10]
		}
		result = append(result, CDNStatPoint{Day: day, Value: point.Value})
	}
	return result
}

func cdnLastValue(points []CDNStatPoint) int64 {
	if len(points) == 0 {
		return 0
	}
	return points[len(points)-1].Value
}

// FetchCDNStats collects the CDN metrics shown on the admin overview. It reads
// the account's domains live so new domains appear without a code change.
func FetchCDNStats(ctx context.Context, secretID, secretKey string, days int, now time.Time) (*CDNStats, error) {
	if secretID == "" || secretKey == "" {
		return nil, fmt.Errorf("cdn credentials are not configured")
	}
	if days < 1 {
		days = 30
	}
	if days > 90 {
		days = 90
	}
	loc := time.FixedZone("CST", 8*3600)
	nowLocal := now.In(loc)
	startLocal := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	start := startLocal.Format("2006-01-02 15:04:05")
	end := nowLocal.Format("2006-01-02 15:04:05")

	domains, err := cdnDomainNames(ctx, secretID, secretKey)
	if err != nil {
		return nil, err
	}
	stats := &CDNStats{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Days:        days,
		Domains:     make([]CDNDomainStats, 0, len(domains)),
	}
	if payType, payErr := cdnPayType(ctx, secretID, secretKey); payErr == nil {
		stats.PayType = payType
	}
	if pkg, pkgErr := cdnTrafficPackage(ctx, secretID, secretKey); pkgErr == nil {
		stats.TrafficPackage = pkg
	}
	if status, statusErr := cdnStatusCounts(ctx, secretID, secretKey, start, end); statusErr == nil {
		stats.Status = status
	}
	if len(domains) == 0 {
		return stats, nil
	}

	flux, err := cdnSeries(ctx, secretID, secretKey, "DescribeCdnData", "flux", domains, start, end)
	if err != nil {
		return nil, err
	}
	requests, err := cdnSeries(ctx, secretID, secretKey, "DescribeCdnData", "request", domains, start, end)
	if err != nil {
		return nil, err
	}
	bandwidth, err := cdnSeries(ctx, secretID, secretKey, "DescribeCdnData", "bandwidth", domains, start, end)
	if err != nil {
		return nil, err
	}
	origin, err := cdnSeries(ctx, secretID, secretKey, "DescribeOriginData", "flux", domains, start, end)
	if err != nil {
		return nil, err
	}

	for _, domain := range domains {
		item := CDNDomainStats{Domain: domain}
		if metric, ok := flux[domain]; ok {
			item.Flux = metric.SummarizedData.Value
			item.FluxSeries = cdnPoints(metric.DetailData)
			item.TodayFlux = cdnLastValue(item.FluxSeries)
		}
		if metric, ok := requests[domain]; ok {
			item.Requests = metric.SummarizedData.Value
			item.RequestSeries = cdnPoints(metric.DetailData)
			item.TodayRequests = cdnLastValue(item.RequestSeries)
		}
		if metric, ok := bandwidth[domain]; ok {
			item.BandwidthPeak = metric.SummarizedData.Value
		}
		if metric, ok := origin[domain]; ok {
			item.OriginFlux = metric.SummarizedData.Value
			item.TodayOriginFlux = cdnLastValue(cdnPoints(metric.DetailData))
		}
		if item.Flux > 0 {
			hit := float64(item.Flux-item.OriginFlux) / float64(item.Flux)
			if hit < 0 {
				hit = 0
			} else if hit > 1 {
				hit = 1
			}
			item.HitRate = hit
		}
		stats.Domains = append(stats.Domains, item)
	}
	return stats, nil
}
