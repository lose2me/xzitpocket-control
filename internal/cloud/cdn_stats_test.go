package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

type cdnStubTransport func(action, body string) string

func (f cdnStubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(req.Body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(f(req.Header.Get("X-TC-Action"), string(raw)))),
		Header:     make(http.Header),
	}, nil
}

func TestFetchCDNStatsAssemblesDomains(t *testing.T) {
	original := cdnAPIClient
	defer func() { cdnAPIClient = original }()

	entry := func(resource, metric, key string, points map[string]int64, summaryName string, summary int64) map[string]any {
		days := make([]string, 0, len(points))
		for day := range points {
			days = append(days, day)
		}
		sort.Strings(days)
		details := make([]any, 0, len(days))
		for _, day := range days {
			details = append(details, map[string]any{"Time": day + " 00:00:00", "Value": points[day]})
		}
		return map[string]any{
			"Resource": resource,
			key: []any{map[string]any{
				"Metric":         metric,
				"DetailData":     details,
				"SummarizedData": map[string]any{"Name": summaryName, "Value": summary},
			}},
		}
	}
	response := func(entries ...any) string {
		return mustJSON(map[string]any{"Response": map[string]any{"Data": entries}})
	}

	cdnAPIClient = &http.Client{Transport: cdnStubTransport(func(action, body string) string {
		var payload struct {
			Metric  string   `json:"Metric"`
			Domains []string `json:"Domains"`
		}
		_ = json.Unmarshal([]byte(body), &payload)
		switch action {
		case "DescribeDomains":
			return mustJSON(map[string]any{"Response": map[string]any{"Domains": []any{
				map[string]any{"Domain": "xuda.live"}, map[string]any{"Domain": "dl.xuda.live"},
			}}})
		case "DescribePayType":
			return `{"Response":{"PayType":"flux"}}`
		case "DescribeTrafficPackages":
			return `{"Response":{"TrafficPackages":[{"Type":"日常流量包","Bytes":100,"BytesUsed":40,"Status":"enabled","TrueExpireTime":"2027-10-10 23:59:59"}]}}`
		case "DescribeCdnData":
			switch payload.Metric {
			case "flux":
				return response(
					entry("xuda.live", "flux", "CdnData", map[string]int64{"2026-10-17": 5}, "sum", 5),
					entry("dl.xuda.live", "flux", "CdnData", map[string]int64{"2026-10-16": 5, "2026-10-17": 20}, "sum", 30),
				)
			case "request":
				return response(
					entry("xuda.live", "request", "CdnData", map[string]int64{"2026-10-17": 7}, "sum", 7),
					entry("dl.xuda.live", "request", "CdnData", map[string]int64{"2026-10-17": 3}, "sum", 3),
				)
			case "bandwidth":
				return response(entry("dl.xuda.live", "bandwidth", "CdnData", map[string]int64{"2026-10-17": 500}, "max", 1234))
			case "2xx", "3xx", "4xx", "5xx":
				return response(entry("multiDomains", payload.Metric, "CdnData", map[string]int64{"2026-10-17": 1}, "sum", 111))
			}
		case "DescribeOriginData":
			return response(
				entry("xuda.live", "flux", "OriginData", map[string]int64{"2026-10-17": 2}, "sum", 2),
				entry("dl.xuda.live", "flux", "OriginData", map[string]int64{"2026-10-16": 2, "2026-10-17": 5}, "sum", 10),
			)
		}
		return `{"Response":{}}`
	})}

	stats, err := FetchCDNStats(context.Background(), "AKIDtest", "secretkey", 7, time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Domains) != 2 || stats.Domains[0].Domain != "dl.xuda.live" {
		t.Fatalf("domains = %#v", stats.Domains)
	}
	dl := stats.Domains[0]
	if dl.Flux != 30 || dl.TodayFlux != 20 || dl.TodayRequests != 3 || dl.OriginFlux != 10 || dl.TodayOriginFlux != 5 {
		t.Fatalf("dl stats = %#v", dl)
	}
	if dl.BandwidthPeak != 1234 {
		t.Fatalf("bandwidth peak = %d", dl.BandwidthPeak)
	}
	if dl.HitRate < 0.66 || dl.HitRate > 0.67 {
		t.Fatalf("hit rate = %v", dl.HitRate)
	}
	if stats.Status.Status2xx != 111 || stats.Status.Status4xx != 111 {
		t.Fatalf("status = %#v", stats.Status)
	}
	if stats.TrafficPackage == nil || stats.TrafficPackage.Remaining != 60 || stats.TrafficPackage.ExpireTime == "" {
		t.Fatalf("traffic package = %#v", stats.TrafficPackage)
	}
	if stats.PayType != "flux" || stats.Days != 7 {
		t.Fatalf("pay type/days = %q/%d", stats.PayType, stats.Days)
	}
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
