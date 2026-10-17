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

// HostSample is one day of a host metric series.
type HostSample struct {
	Day   string  `json:"day"`
	Value float64 `json:"value"`
}

// HostTrafficPackage is the Lighthouse monthly traffic package.
type HostTrafficPackage struct {
	Used       int64  `json:"used"`
	Total      int64  `json:"total"`
	Remaining  int64  `json:"remaining"`
	ExpireTime string `json:"expire_time"`
}

// HostStats is the local Lighthouse machine's statistics for the overview.
type HostStats struct {
	GeneratedAt    string              `json:"generated_at"`
	Days           int                 `json:"days"`
	InstanceID     string              `json:"instance_id"`
	InstanceName   string              `json:"instance_name"`
	Region         string              `json:"region"`
	BandwidthCap   float64             `json:"bandwidth_cap"`
	CPU            float64             `json:"cpu"`
	Mem            float64             `json:"mem"`
	Disk           float64             `json:"disk"`
	OutBandwidth   float64             `json:"out_bandwidth"`
	InBandwidth    float64             `json:"in_bandwidth"`
	OutRatio       float64             `json:"out_ratio"`
	OutSeries      []HostSample        `json:"out_series"`
	TrafficPackage *HostTrafficPackage `json:"traffic_package,omitempty"`
}

type monitorPoint struct {
	Timestamps []int64   `json:"Timestamps"`
	Values     []float64 `json:"Values"`
}

type monitorResponse struct {
	Response struct {
		Error      *cdnAPIError   `json:"Error"`
		DataPoints []monitorPoint `json:"DataPoints"`
	} `json:"Response"`
}

type metricSeries struct {
	timestamps []int64
	values     []float64
}

func txCall(ctx context.Context, secretID, secretKey, service, host, action, version, region string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	timestamp := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", tc3Authorization(secretID, secretKey, service, host, action, string(body), timestamp))
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))
	if region != "" {
		req.Header.Set("X-TC-Region", region)
	}

	resp, err := cdnAPIClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s: %s", action, resp.Status, strings.TrimSpace(string(raw)))
	}
	var envelope struct {
		Response struct {
			Error *cdnAPIError `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", action, err)
	}
	if envelope.Response.Error != nil {
		return nil, fmt.Errorf("%s: %s: %s", action, envelope.Response.Error.Code, envelope.Response.Error.Message)
	}
	return raw, nil
}

// lighthouseInstance resolves the instance by its public IP so no instance id
// has to be configured.
func lighthouseInstance(ctx context.Context, secretID, secretKey, region, publicIP string) (string, string, float64, error) {
	raw, err := txCall(ctx, secretID, secretKey, "lighthouse", "lighthouse.tencentcloudapi.com",
		"DescribeInstances", "2020-03-24", region, map[string]any{"Limit": 100})
	if err != nil {
		return "", "", 0, err
	}
	var resp struct {
		Response struct {
			InstanceSet []struct {
				InstanceID         string   `json:"InstanceId"`
				InstanceName       string   `json:"InstanceName"`
				PublicAddresses    []string `json:"PublicAddresses"`
				InternetAccessible struct {
					MaxBandwidthOut int64 `json:"InternetMaxBandwidthOut"`
				} `json:"InternetAccessible"`
			} `json:"InstanceSet"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", 0, err
	}
	for _, inst := range resp.Response.InstanceSet {
		for _, ip := range inst.PublicAddresses {
			if ip == publicIP {
				return inst.InstanceID, inst.InstanceName, float64(inst.InternetAccessible.MaxBandwidthOut), nil
			}
		}
	}
	return "", "", 0, fmt.Errorf("lighthouse instance for %s not found", publicIP)
}

func lighthouseTrafficPackage(ctx context.Context, secretID, secretKey, region, instanceID string) (*HostTrafficPackage, error) {
	raw, err := txCall(ctx, secretID, secretKey, "lighthouse", "lighthouse.tencentcloudapi.com",
		"DescribeInstancesTrafficPackages", "2020-03-24", region, map[string]any{"InstanceIds": []string{instanceID}})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Response struct {
			InstanceTrafficPackageSet []struct {
				InstanceID        string `json:"InstanceId"`
				TrafficPackageSet []struct {
					TrafficUsed              int64  `json:"TrafficUsed"`
					TrafficPackageTotal      int64  `json:"TrafficPackageTotal"`
					TrafficPackageRemaining  int64  `json:"TrafficPackageRemaining"`
					EndTime                  string `json:"EndTime"`
				} `json:"TrafficPackageSet"`
			} `json:"InstanceTrafficPackageSet"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	for _, item := range resp.Response.InstanceTrafficPackageSet {
		if item.InstanceID != instanceID || len(item.TrafficPackageSet) == 0 {
			continue
		}
		pkg := &HostTrafficPackage{}
		for _, p := range item.TrafficPackageSet {
			pkg.Used += p.TrafficUsed
			pkg.Total += p.TrafficPackageTotal
			pkg.Remaining += p.TrafficPackageRemaining
			if p.EndTime != "" && (pkg.ExpireTime == "" || p.EndTime < pkg.ExpireTime) {
				pkg.ExpireTime = p.EndTime
			}
		}
		return pkg, nil
	}
	return nil, nil
}

func monitorMetric(ctx context.Context, secretID, secretKey, region, instanceID, metric, start, end string) ([]int64, []float64, error) {
	raw, err := txCall(ctx, secretID, secretKey, "monitor", "monitor.tencentcloudapi.com",
		"GetMonitorData", "2018-07-24", region, map[string]any{
			"Namespace":  "QCE/LIGHTHOUSE",
			"MetricName": metric,
			"Instances":  []any{map[string]any{"Dimensions": []any{map[string]any{"Name": "InstanceId", "Value": instanceID}}}},
			"Period":     3600,
			"StartTime":  start,
			"EndTime":    end,
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

func lastValue(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[len(values)-1]
}

func dailyMax(timestamps []int64, values []float64, loc *time.Location) []HostSample {
	byDay := make(map[string]float64)
	for i, ts := range timestamps {
		if i >= len(values) {
			break
		}
		day := time.Unix(ts, 0).In(loc).Format("2006-01-02")
		if existing, ok := byDay[day]; !ok || values[i] > existing {
			byDay[day] = values[i]
		}
	}
	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)
	series := make([]HostSample, 0, len(days))
	for _, day := range days {
		series = append(series, HostSample{Day: day, Value: byDay[day]})
	}
	return series
}

// FetchHostStats reads this Lighthouse machine's monitoring data from Tencent
// Cloud. The instance is resolved by its public IP.
func FetchHostStats(ctx context.Context, secretID, secretKey, region, publicIP string, days int, now time.Time) (*HostStats, error) {
	if secretID == "" || secretKey == "" {
		return nil, fmt.Errorf("monitor credentials are not configured")
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

	instanceID, instanceName, bandwidthCap, err := lighthouseInstance(ctx, secretID, secretKey, region, publicIP)
	if err != nil {
		return nil, err
	}
	stats := &HostStats{
		GeneratedAt:  now.UTC().Format(time.RFC3339),
		Days:         days,
		InstanceID:   instanceID,
		InstanceName: instanceName,
		Region:       region,
		BandwidthCap: bandwidthCap,
	}
	if pkg, pkgErr := lighthouseTrafficPackage(ctx, secretID, secretKey, region, instanceID); pkgErr == nil {
		stats.TrafficPackage = pkg
	}

	metrics := map[string]string{
		"cpu":   "CpuUsage",
		"mem":   "MemUsage",
		"disk":  "DiskUsage",
		"out":   "LighthouseOuttraffic",
		"in":    "LighthouseIntraffic",
		"ratio": "LighthouseOutratio",
	}
	series := make(map[string]metricSeries, len(metrics))
	for key, metric := range metrics {
		timestamps, values, metricErr := monitorMetric(ctx, secretID, secretKey, region, instanceID, metric, start, end)
		if metricErr != nil {
			return nil, metricErr
		}
		series[key] = metricSeries{timestamps: timestamps, values: values}
	}
	stats.CPU = lastValue(series["cpu"].values)
	stats.Mem = lastValue(series["mem"].values)
	stats.Disk = lastValue(series["disk"].values)
	stats.OutBandwidth = lastValue(series["out"].values)
	stats.InBandwidth = lastValue(series["in"].values)
	stats.OutRatio = lastValue(series["ratio"].values)
	stats.OutSeries = dailyMax(series["out"].timestamps, series["out"].values, loc)
	return stats, nil
}
