package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stubTencentCloud routes the SDK-like HTTP calls by their X-TC-Action header.
func stubTencentCloud(t *testing.T, handle func(action, body string) (int, string)) *[]string {
	t.Helper()
	original := cdnAPIClient
	calls := &[]string{}
	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		action := req.Header.Get("X-TC-Action")
		*calls = append(*calls, action)
		status, body := handle(action, string(raw))
		return stubResponse(status, body), nil
	})}
	t.Cleanup(func() { cdnAPIClient = original })
	return calls
}

func monitorPoints(ts ...int64) string {
	values := make([]float64, len(ts))
	for i := range ts {
		values[i] = float64(i + 1)
	}
	payload, _ := json.Marshal(map[string]any{"Response": map[string]any{"DataPoints": []any{
		map[string]any{"Timestamps": ts, "Values": values},
	}}})
	return string(payload)
}

func TestTxCallSurfacesTencentErrors(t *testing.T) {
	ctx := context.Background()
	original := cdnAPIClient
	t.Cleanup(func() { cdnAPIClient = original })

	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	if _, err := txCall(ctx, "id", "key", "cvm", "cvm.tencentcloudapi.com", "DescribeInstances", "2017-03-12", "", map[string]any{}); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("network error = %v", err)
	}

	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stubResponse(http.StatusForbidden, "denied"), nil
	})}
	if _, err := txCall(ctx, "id", "key", "cvm", "cvm.tencentcloudapi.com", "DescribeInstances", "2017-03-12", "", map[string]any{}); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("status error = %v", err)
	}

	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK, "<html>"), nil
	})}
	if _, err := txCall(ctx, "id", "key", "cvm", "cvm.tencentcloudapi.com", "DescribeInstances", "2017-03-12", "", map[string]any{}); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("decode error = %v", err)
	}

	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK, `{"Response":{"Error":{"Code":"AuthFailure","Message":"bad secret"}}}`), nil
	})}
	if _, err := txCall(ctx, "id", "key", "cvm", "cvm.tencentcloudapi.com", "DescribeInstances", "2017-03-12", "", map[string]any{}); err == nil || !strings.Contains(err.Error(), "bad secret") {
		t.Fatalf("api error = %v", err)
	}

	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK, `{"Response":{"RequestId":"x"}}`), nil
	})}
	raw, err := txCall(ctx, "id", "key", "cvm", "cvm.tencentcloudapi.com", "DescribeInstances", "2017-03-12", "ap-shanghai", map[string]any{"Limit": 1})
	if err != nil || !strings.Contains(string(raw), "Response") {
		t.Fatalf("success = %q, %v", raw, err)
	}
}

func TestFetchHostStatsReadsInstancePackageAndMetrics(t *testing.T) {
	day := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	calls := stubTencentCloud(t, func(action, body string) (int, string) {
		switch action {
		case "DescribeInstances":
			return 200, `{"Response":{"InstanceSet":[
				{"InstanceId":"lhins-other","InstanceName":"other","PublicAddresses":["10.0.0.1"],"InternetAccessible":{"InternetMaxBandwidthOut":2}},
				{"InstanceId":"lhins-6ihiq8cq","InstanceName":"VM-0-14-debian","PublicAddresses":["124.222.103.130"],"InternetAccessible":{"InternetMaxBandwidthOut":4}}
			]}}`
		case "DescribeInstancesTrafficPackages":
			return 200, `{"Response":{"InstanceTrafficPackageSet":[
				{"InstanceId":"lhins-other","TrafficPackageSet":[{"TrafficPackageTotal":1}]},
				{"InstanceId":"lhins-6ihiq8cq","TrafficPackageSet":[
					{"TrafficUsed":100,"TrafficPackageTotal":300,"TrafficPackageRemaining":200,"EndTime":"2026-03-01T00:00:00Z"},
					{"TrafficUsed":50,"TrafficPackageTotal":100,"TrafficPackageRemaining":50,"EndTime":"2026-02-01T00:00:00Z"}
				]}
			]}}`
		case "GetMonitorData":
			if strings.Contains(body, "CpuUsage") || strings.Contains(body, "LighthouseOuttraffic") {
				return 200, monitorPoints(day.Add(-2*time.Hour).Unix(), day.Unix(), day.Add(time.Hour).Unix())
			}
			return 200, monitorPoints(day.Unix())
		}
		return 404, `{"Response":{"Error":{"Code":"UnknownAction","Message":"unknown"}}}`
	})

	stats, err := FetchHostStats(context.Background(), "id", "key", "ap-shanghai", "124.222.103.130", 99, day)
	if err != nil {
		t.Fatal(err)
	}
	if stats.InstanceID != "lhins-6ihiq8cq" || stats.InstanceName != "VM-0-14-debian" || stats.BandwidthCap != 4 {
		t.Fatalf("instance = %+v", stats)
	}
	if stats.Days != 30 {
		t.Fatalf("days = %d", stats.Days)
	}
	if stats.TrafficPackage == nil || stats.TrafficPackage.Total != 400 || stats.TrafficPackage.Used != 150 || stats.TrafficPackage.Remaining != 250 {
		t.Fatalf("package = %+v", stats.TrafficPackage)
	}
	if stats.TrafficPackage.ExpireTime != "2026-02-01T00:00:00Z" {
		t.Fatalf("earliest expiry = %q", stats.TrafficPackage.ExpireTime)
	}
	if stats.CPU != 3 || stats.Mem != 1 || stats.Disk != 1 || stats.OutBandwidth != 3 {
		t.Fatalf("metrics = %+v", stats)
	}
	if len(stats.OutSeries) != 1 || stats.OutSeries[0].Value != 3 {
		t.Fatalf("daily max series = %+v", stats.OutSeries)
	}
	if len(*calls) != 8 {
		t.Fatalf("calls = %v", *calls)
	}
	if stats.GeneratedAt != day.Format(time.RFC3339) {
		t.Fatalf("generated at = %q", stats.GeneratedAt)
	}
}

func TestFetchHostStatsErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := FetchHostStats(ctx, "", "", "ap-shanghai", "1.1.1.1", 30, time.Now()); err == nil {
		t.Fatal("missing credentials should fail")
	}
	stubTencentCloud(t, func(action, body string) (int, string) {
		return 200, `{"Response":{"InstanceSet":[{"InstanceId":"other","PublicAddresses":["9.9.9.9"]}]}}`
	})
	if _, err := FetchHostStats(ctx, "id", "key", "ap-shanghai", "1.1.1.1", 0, time.Now()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing instance error = %v", err)
	}

	stubTencentCloud(t, func(action, body string) (int, string) {
		switch action {
		case "DescribeInstances":
			return 200, `{"Response":{"InstanceSet":[{"InstanceId":"lhins-1","PublicAddresses":["1.1.1.1"]}]}}`
		case "DescribeInstancesTrafficPackages":
			return 200, `{"Response":{"InstanceTrafficPackageSet":[]}}`
		case "GetMonitorData":
			return 200, `{"Response":{"DataPoints":[]}}`
		}
		return 500, "boom"
	})
	stats, err := FetchHostStats(ctx, "id", "key", "ap-shanghai", "1.1.1.1", 30, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TrafficPackage != nil {
		t.Fatalf("missing package should stay nil: %+v", stats.TrafficPackage)
	}
	if stats.CPU != 0 || stats.OutSeries == nil || len(stats.OutSeries) != 0 {
		t.Fatalf("empty metrics = %+v", stats)
	}

	stubTencentCloud(t, func(action, body string) (int, string) {
		if action == "DescribeInstances" {
			return 200, `{"Response":{"InstanceSet":[{"InstanceId":"lhins-1","PublicAddresses":["1.1.1.1"]}]}}`
		}
		return 200, `{"Response":{"Error":{"Code":"InternalError","Message":"monitor down"}}}`
	})
	if _, err := FetchHostStats(ctx, "id", "key", "ap-shanghai", "1.1.1.1", 30, time.Now()); err == nil || !strings.Contains(err.Error(), "monitor down") {
		t.Fatalf("metric error = %v", err)
	}
}

func TestFetchBucketStats(t *testing.T) {
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := FetchBucketStats(context.Background(), "", "", "ap-shanghai", "b", 30, day); err == nil {
		t.Fatal("missing credentials should fail")
	}
	if got := bucketAppID("xzitpocket-assets-1330114136"); got != "1330114136" {
		t.Fatalf("bucketAppID = %q", got)
	}
	if got := bucketAppID("nohyphen"); got != "" {
		t.Fatalf("bucketAppID without appid = %q", got)
	}

	calls := stubTencentCloud(t, func(action, body string) (int, string) {
		if action != "GetMonitorData" {
			return 500, "unexpected"
		}
		switch {
		case strings.Contains(body, "StdStorage"):
			return 200, `{"Response":{"DataPoints":[{"Timestamps":[1],"Values":[12.5]}]}}`
		case strings.Contains(body, "StdObjectNumber"):
			return 200, `{"Response":{"DataPoints":[{"Timestamps":[1],"Values":[7]}]}}`
		case strings.Contains(body, "CdnOriginTraffic"):
			return 200, `{"Response":{"DataPoints":[{"Timestamps":[` + itoa(day.Unix()) + `,` + itoa(day.Add(24*time.Hour).Unix()) + `],"Values":[10,20]}]}}`
		}
		return 200, `{"Response":{"DataPoints":[{"Timestamps":[1],"Values":[2]}]}}`
	})
	stats, err := FetchBucketStats(context.Background(), "id", "key", "ap-shanghai", "xzitpocket-assets-1330114136", 0, day)
	if err != nil {
		t.Fatal(err)
	}
	if stats.StdStorageMB != 12.5 || stats.ObjectNumber != 7 {
		t.Fatalf("storage = %+v", stats)
	}
	if stats.TotalRequests != 2 || stats.CdnOriginTraffic != 30 || stats.InboundTraffic != 2 || stats.InternetTraffic != 2 {
		t.Fatalf("sums = %+v", stats)
	}
	if stats.Status2xx != 2 || stats.Status4xx != 2 || stats.Status5xx != 2 {
		t.Fatalf("status sums = %+v", stats)
	}
	if len(stats.OriginSeries) != 2 || stats.OriginSeries[0].Value != 10 || stats.OriginSeries[1].Value != 20 {
		t.Fatalf("origin series = %+v", stats.OriginSeries)
	}
	if len(*calls) != 9 {
		t.Fatalf("calls = %v", *calls)
	}

	stubTencentCloud(t, func(action, body string) (int, string) {
		return 200, `{"Response":{"Error":{"Code":"LimitExceeded","Message":"too many"}}}`
	})
	if _, err := FetchBucketStats(context.Background(), "id", "key", "ap-shanghai", "b", 30, day); err == nil {
		t.Fatal("metric failure should abort")
	}
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

func TestCdnStatHelpers(t *testing.T) {
	if cdnFindMetric([]cdnMetric{{Metric: "flux"}}, "request") != nil {
		t.Fatal("missing metric should be nil")
	}
	if found := cdnFindMetric([]cdnMetric{{Metric: "flux"}, {Metric: "request"}}, "request"); found == nil || found.Metric != "request" {
		t.Fatalf("cdnFindMetric = %+v", found)
	}
	points := cdnPoints([]cdnMetricPoint{{Time: "2026-01-02 03:04:05", Value: 5}, {Time: "short", Value: 6}})
	if len(points) != 2 || points[0].Day != "2026-01-02" || points[1].Day != "short" {
		t.Fatalf("cdnPoints = %+v", points)
	}
	if cdnLastValue(nil) != 0 || cdnLastValue(points) != 6 {
		t.Fatalf("cdnLastValue = %d", cdnLastValue(points))
	}
	if sumValues(nil) != 0 || sumValues([]float64{1, 2}) != 3 {
		t.Fatal("sumValues mismatch")
	}
	if lastValue(nil) != 0 || lastValue([]float64{4, 5}) != 5 {
		t.Fatal("lastValue mismatch")
	}
	series := dailyMax([]int64{1, 2, 3}, []float64{9}, time.UTC)
	if len(series) != 1 {
		t.Fatalf("dailyMax should ignore values without timestamps: %+v", series)
	}
}

func TestFetchCdnStatsBuildsDomainRows(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	if _, err := FetchCDNStats(context.Background(), "", "", 30, now); err == nil {
		t.Fatal("missing credentials should fail")
	}

	domainCall := func() string {
		return `{"Response":{"Domains":[{"Domain":"xuda.live"},{"Domain":"dl.xuda.live"},{"Domain":""}]}}`
	}
	seriesCall := func(action, body string) string {
		if action == "DescribeOriginData" {
			return `{"Response":{"Data":[{"Resource":"dl.xuda.live","OriginData":[{"Metric":"flux","SummarizedData":{"Value":10},"DetailData":[{"Time":"2026-01-01 00:00:00","Value":10}]}]}]}}`
		}
		switch {
		case strings.Contains(body, `"Metric":"flux"`):
			return `{"Response":{"Data":[{"Resource":"dl.xuda.live","CdnData":[{"Metric":"flux","SummarizedData":{"Value":100},"DetailData":[{"Time":"2026-01-01 00:00:00","Value":40},{"Time":"2026-01-02 00:00:00","Value":60}]}]}]}}`
		case strings.Contains(body, `"Metric":"request"`):
			return `{"Response":{"Data":[{"Resource":"dl.xuda.live","CdnData":[{"Metric":"request","SummarizedData":{"Value":7},"DetailData":[{"Time":"2026-01-02 00:00:00","Value":7}]}]}]}}`
		case strings.Contains(body, `"Metric":"bandwidth"`):
			return `{"Response":{"Data":[{"Resource":"dl.xuda.live","CdnData":[{"Metric":"bandwidth","SummarizedData":{"Value":900}}]}]}}`
		}
		return `{"Response":{"Data":[]}}`
	}

	stubTencentCloud(t, func(action, body string) (int, string) {
		switch action {
		case "DescribeDomains":
			return 200, domainCall()
		case "DescribePayType":
			return 200, `{"Response":{"PayType":"flux"}}`
		case "DescribeTrafficPackages":
			return 200, `{"Response":{"TrafficPackages":[
				{"Status":"disabled","Bytes":1000,"BytesUsed":10},
				{"Status":"enabled","Bytes":2000,"BytesUsed":500,"TrueExpireTime":"2026-03-01"},
				{"Status":"enabled","Bytes":100,"BytesUsed":300,"ExpireTime":"2026-02-01"}
			]}}`
		case "DescribeCdnData":
			if strings.Contains(body, `"Metric":"2xx"`) {
				return 200, `{"Response":{"Data":[{"CdnData":[{"Metric":"2xx","SummarizedData":{"Value":11}}]}]}}`
			}
			if strings.Contains(body, `"Metric":"3xx"`) || strings.Contains(body, `"Metric":"4xx"`) || strings.Contains(body, `"Metric":"5xx"`) {
				return 200, `{"Response":{"Data":[{"CdnData":[{"Metric":"4xx","SummarizedData":{"Value":3}}]}]}}`
			}
			return 200, seriesCall(action, body)
		case "DescribeOriginData":
			return 200, seriesCall(action, body)
		}
		return 500, "unexpected"
	})

	stats, err := FetchCDNStats(context.Background(), "id", "key", 500, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Days != 90 || stats.PayType != "flux" {
		t.Fatalf("stats header = %+v", stats)
	}
	if stats.TrafficPackage == nil || stats.TrafficPackage.Count != 2 || stats.TrafficPackage.Total != 2100 || stats.TrafficPackage.Remaining != 1300 {
		t.Fatalf("traffic package = %+v", stats.TrafficPackage)
	}
	if stats.TrafficPackage.ExpireTime != "2026-02-01" {
		t.Fatalf("package expiry = %q", stats.TrafficPackage.ExpireTime)
	}
	if stats.Status.Status2xx != 11 || stats.Status.Status3xx != 0 || stats.Status.Status4xx != 3 {
		t.Fatalf("status = %+v", stats.Status)
	}
	if len(stats.Domains) != 2 {
		t.Fatalf("domains = %+v", stats.Domains)
	}
	dl := stats.Domains[0]
	if dl.Domain != "dl.xuda.live" || dl.Flux != 100 || dl.TodayFlux != 60 || dl.Requests != 7 || dl.TodayRequests != 7 || dl.BandwidthPeak != 900 {
		t.Fatalf("dl row = %+v", dl)
	}
	if dl.OriginFlux != 10 || dl.TodayOriginFlux != 10 || dl.HitRate != 0.9 {
		t.Fatalf("dl origin = %+v", dl)
	}
	if stats.Domains[1].Domain != "xuda.live" || stats.Domains[1].HitRate != 0 {
		t.Fatalf("empty domain row = %+v", stats.Domains[1])
	}
}

func TestFetchCdnStatsEdgeCases(t *testing.T) {
	now := time.Now()
	stubTencentCloud(t, func(action, body string) (int, string) {
		switch action {
		case "DescribeDomains":
			return 200, `{"Response":{"Domains":[]}}`
		case "DescribePayType":
			return 500, "no pay type"
		case "DescribeTrafficPackages":
			return 200, `{"Response":{"TrafficPackages":[{"Status":"disabled"}]}}`
		case "DescribeCdnData":
			return 500, "status metrics down"
		}
		return 500, "unexpected"
	})
	stats, err := FetchCDNStats(context.Background(), "id", "key", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Days != 30 || stats.PayType != "" || stats.TrafficPackage != nil || len(stats.Domains) != 0 {
		t.Fatalf("empty stats = %+v", stats)
	}

	stubTencentCloud(t, func(action, body string) (int, string) {
		if action == "DescribeDomains" {
			return 200, `{"Response":{"Domains":[{"Domain":"dl.xuda.live"}]}}`
		}
		return 200, `{"Response":{"Error":{"Code":"LimitExceeded","Message":"slow down"}}}`
	})
	if _, err := FetchCDNStats(context.Background(), "id", "key", 30, now); err == nil {
		t.Fatal("series failure should abort")
	}

	stubTencentCloud(t, func(action, body string) (int, string) {
		switch action {
		case "DescribeDomains":
			return 200, `{"Response":{"Domains":[{"Domain":"dl.xuda.live"}]}}`
		case "DescribePayType", "DescribeTrafficPackages":
			return 200, `{"Response":{}}`
		case "DescribeCdnData":
			if strings.Contains(body, `"Metric":"flux"`) {
				return 200, `{"Response":{"Data":[{"Resource":"dl.xuda.live","CdnData":[{"Metric":"flux","SummarizedData":{"Value":10},"DetailData":[{"Time":"2026-01-01","Value":10}]}]}]}}`
			}
			return 200, `{"Response":{"Data":[]}}`
		case "DescribeOriginData":
			return 200, `{"Response":{"Data":[{"Resource":"dl.xuda.live","OriginData":[{"Metric":"flux","SummarizedData":{"Value":30}}]}]}}`
		}
		return 500, "unexpected"
	})
	stats, err = FetchCDNStats(context.Background(), "id", "key", 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Domains[0].HitRate != 0 {
		t.Fatalf("negative hit rate should clamp to 0: %+v", stats.Domains[0])
	}

	stubTencentCloud(t, func(action, body string) (int, string) {
		switch action {
		case "DescribeDomains":
			return 200, `{"Response":{"Domains":[{"Domain":"dl.xuda.live"}]}}`
		case "DescribeTrafficPackages":
			return 200, `{"Response":{"TrafficPackages":[{"Status":"enabled","Bytes":100,"BytesUsed":90,"TrueExpireTime":"2026-01-01"}]}}`
		case "DescribeCdnData", "DescribeOriginData":
			return 200, `{"Response":{"Data":[]}}`
		}
		return 200, `{"Response":{}}`
	})
	stats, err = FetchCDNStats(context.Background(), "id", "key", 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TrafficPackage == nil || stats.TrafficPackage.Remaining != 10 {
		t.Fatalf("package = %+v", stats.TrafficPackage)
	}
}
