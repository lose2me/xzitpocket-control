package cloud

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func stubResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// The expected values below were computed independently from the documented
// Tencent algorithms, so a change in the signing code is caught here.

func TestCosAuthorizationMatchesKnownSignature(t *testing.T) {
	auth := cosAuthorization(
		"AKIDtest",
		"secretkey",
		"1700000000;1700003600",
		"put",
		"/books/x/original.pdf",
		"content-type;host",
		"content-type=application%2Fpdf&host=bucket.cos.ap-shanghai.myqcloud.com",
	)
	const want = "q-sign-algorithm=sha1" +
		"&q-ak=AKIDtest" +
		"&q-sign-time=1700000000;1700003600" +
		"&q-key-time=1700000000;1700003600" +
		"&q-header-list=content-type;host" +
		"&q-url-param-list=" +
		"&q-signature=743ab9532d9008f5834cdb943fbf1d55e6571445"
	if auth != want {
		t.Fatalf("cosAuthorization mismatch:\n got %q\nwant %q", auth, want)
	}
}

func TestCosDeleteAuthorizationSignsHostOnly(t *testing.T) {
	headerList, headerString := cosHeaderSignature(map[string]string{"host": "bucket.cos.ap-shanghai.myqcloud.com"})
	auth := cosAuthorization("AKIDtest", "secretkey", "1700000000;1700003600",
		"delete", "/books/x/original.pdf", headerList, headerString)
	const want = "q-sign-algorithm=sha1" +
		"&q-ak=AKIDtest" +
		"&q-sign-time=1700000000;1700003600" +
		"&q-key-time=1700000000;1700003600" +
		"&q-header-list=host" +
		"&q-url-param-list=" +
		"&q-signature=6d4dce6a0ccf3d210b50adecc15ce8b8b57bc84a"
	if auth != want {
		t.Fatalf("delete authorization mismatch:\n got %q\nwant %q", auth, want)
	}
}

func TestCdnSignMatchesKnownValue(t *testing.T) {
	if got := cdnSign("testcdnkey", "/books/x/original.pdf", 1700000000); got != "433522f3de0c4a5ecd0cec74270be90c" {
		t.Fatalf("cdnSign = %q, want 433522f3de0c4a5ecd0cec74270be90c", got)
	}
}

func TestTc3AuthorizationMatchesKnownValue(t *testing.T) {
	auth := tc3Authorization(
		"AKIDz8krbsJ5yKBZQpn74WFkmLPx3*******",
		"Gu5t9xGARNpq86cd98joQYCN3*******",
		"cvm",
		"cvm.tencentcloudapi.com",
		"DescribeInstances",
		`{"Limit": 1, "Filters": [{"Values": ["\u672a\u547d\u540d"], "Name": "instance-name"}]}`,
		1551113065,
	)
	// The canonical-request payload hash of this input is the one published in
	// the Tencent Cloud TC3 example (35e9c5b0...); only the example's masked
	// SecretKey differs, so the signature below is deterministic.
	const want = "TC3-HMAC-SHA256 Credential=AKIDz8krbsJ5yKBZQpn74WFkmLPx3*******/2019-02-25/cvm/tc3_request, " +
		"SignedHeaders=content-type;host;x-tc-action, " +
		"Signature=be4f67d323c78ab9acb7395e43c0dbcf822a9cfac32fea2449a7bc7726b770a3"
	if auth != want {
		t.Fatalf("tc3Authorization mismatch:\n got %q\nwant %q", auth, want)
	}
}

func TestPresignPutURL(t *testing.T) {
	client := NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	raw, err := client.PresignPutURL("submissions/sub_1/original.pdf", 12, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "bucket-1250000000.cos.ap-shanghai.myqcloud.com" ||
		parsed.Path != "/submissions/sub_1/original.pdf" {
		t.Fatalf("url = %q", raw)
	}
	query := parsed.Query()
	if query.Get("q-sign-algorithm") != "sha1" || query.Get("q-ak") != "AKIDtest" ||
		query.Get("q-header-list") != "content-length;host" || query.Get("q-url-param-list") != "" {
		t.Fatalf("query = %#v", query)
	}
	if len(query.Get("q-signature")) != 40 {
		t.Fatalf("signature = %q", query.Get("q-signature"))
	}
}

func TestGetObjectRangeSendsSignedRange(t *testing.T) {
	var captured *http.Request
	client := NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		captured = req
		return stubResponse(http.StatusPartialContent, "%PDF-"), nil
	})}
	data, err := client.GetObjectRange(context.Background(), "submissions/x/original.pdf", 0, 4)
	if err != nil {
		t.Fatalf("GetObjectRange: %v", err)
	}
	if string(data) != "%PDF-" {
		t.Fatalf("data = %q", data)
	}
	if captured == nil || captured.Method != http.MethodGet {
		t.Fatalf("request = %+v", captured)
	}
	if captured.Header.Get("Range") != "bytes=0-4" {
		t.Fatalf("range = %q", captured.Header.Get("Range"))
	}
	if auth := captured.Header.Get("Authorization"); !strings.Contains(auth, "q-header-list=host;range") {
		t.Fatalf("authorization should sign host and range: %q", auth)
	}
}

func TestCosDeleteObjectSendsSignedDelete(t *testing.T) {
	var captured *http.Request
	client := NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		captured = req
		return stubResponse(http.StatusNoContent, ""), nil
	})}
	if err := client.DeleteObject(context.Background(), "books/x/original.pdf"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if captured == nil || captured.Method != http.MethodDelete {
		t.Fatalf("request = %+v", captured)
	}
	if captured.URL.Path != "/books/x/original.pdf" {
		t.Fatalf("path = %q", captured.URL.Path)
	}
	if auth := captured.Header.Get("Authorization"); !strings.Contains(auth, "q-header-list=host") {
		t.Fatalf("authorization should sign only host: %q", auth)
	}
}

func TestCosDeleteObjectReportsFailure(t *testing.T) {
	client := NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stubResponse(http.StatusForbidden, "<Error><Code>AccessDenied</Code></Error>"), nil
	})}
	if err := client.DeleteObject(context.Background(), "books/x/original.pdf"); err == nil {
		t.Fatal("expected an error for a 403 response")
	}
}

func TestPurgeCDNCacheSendsPurgeRequest(t *testing.T) {
	original := cdnAPIClient
	defer func() { cdnAPIClient = original }()
	var captured *http.Request
	var body string
	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		captured = req
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
		return stubResponse(http.StatusOK, `{"Response": {"RequestId": "x"}}`), nil
	})}

	err := PurgeCDNCache(context.Background(), "AKIDtest", "secretkey",
		[]string{"https://dl.xuda.live/books/x/original.pdf"})
	if err != nil {
		t.Fatalf("PurgeCDNCache: %v", err)
	}
	if captured == nil || captured.Method != http.MethodPost || captured.URL.Host != "cdn.tencentcloudapi.com" {
		t.Fatalf("request = %+v", captured)
	}
	if captured.Header.Get("X-TC-Action") != "PurgeUrlsCache" || captured.Header.Get("X-TC-Version") != "2018-06-06" {
		t.Fatalf("unexpected TC headers: %v", captured.Header)
	}
	if !strings.HasPrefix(captured.Header.Get("Authorization"), "TC3-HMAC-SHA256 ") {
		t.Fatalf("authorization = %q", captured.Header.Get("Authorization"))
	}
	if !strings.Contains(body, "https://dl.xuda.live/books/x/original.pdf") {
		t.Fatalf("body = %q", body)
	}
}

func TestPurgeCDNCacheReportsAPIError(t *testing.T) {
	original := cdnAPIClient
	defer func() { cdnAPIClient = original }()
	cdnAPIClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK, `{"Response": {"Error": {"Code": "InvalidParameter", "Message": "bad url"}}}`), nil
	})}
	if err := PurgeCDNCache(context.Background(), "AKIDtest", "secretkey", []string{"https://dl.xuda.live/x"}); err == nil {
		t.Fatal("expected the API error to surface")
	}
}

func TestPurgeCDNCacheSkipsWithoutCredentials(t *testing.T) {
	if err := PurgeCDNCache(context.Background(), "", "", []string{"https://dl.xuda.live/x"}); err != nil {
		t.Fatalf("PurgeCDNCache: %v", err)
	}
	if err := PurgeCDNCache(context.Background(), "AKIDtest", "secretkey", nil); err != nil {
		t.Fatalf("PurgeCDNCache: %v", err)
	}
}
