package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newStubCOS(t *testing.T, handle func(*http.Request) (*http.Response, error)) (*COSClient, *http.Request) {
	t.Helper()
	client := NewCOSClient("AKIDtest", "secretkey", "bucket-1250000000", "ap-shanghai")
	captured := &http.Request{}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		*captured = *req
		return handle(req)
	})}
	return client, captured
}

func TestCosObjectLifecycle(t *testing.T) {
	ctx := context.Background()
	client, captured := newStubCOS(t, func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodPut:
			return stubResponse(http.StatusOK, ""), nil
		case http.MethodGet:
			resp := stubResponse(http.StatusOK, "%PDF-1.7 body")
			resp.ContentLength = 13
			return resp, nil
		case http.MethodHead:
			resp := stubResponse(http.StatusOK, "")
			resp.ContentLength = 4096
			resp.Header.Set("ETag", `"abc123"`)
			return resp, nil
		case http.MethodDelete:
			return stubResponse(http.StatusNoContent, ""), nil
		}
		return stubResponse(http.StatusNotImplemented, ""), nil
	})

	if err := client.PutObject(ctx, "/books/x/original.pdf", "application/pdf", []byte("%PDF")); err != nil {
		t.Fatal(err)
	}
	if captured.Method != http.MethodPut || captured.URL.Path != "/books/x/original.pdf" || captured.Host != "bucket-1250000000.cos.ap-shanghai.myqcloud.com" {
		t.Fatalf("put request = %+v", captured)
	}
	if !strings.Contains(captured.Header.Get("Authorization"), "q-header-list=content-type;host") {
		t.Fatalf("put authorization = %q", captured.Header.Get("Authorization"))
	}

	stream, err := client.GetObject(ctx, "books/x/original.pdf")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(stream.Body)
	stream.Body.Close()
	if string(body) != "%PDF-1.7 body" {
		t.Fatalf("get body = %q", body)
	}

	size, err := client.HeadObject(ctx, "books/x/original.pdf")
	if err != nil || size != 4096 {
		t.Fatalf("head = %d, %v", size, err)
	}
	size, etag, err := client.HeadObjectInfo(ctx, "books/x/original.pdf")
	if err != nil || size != 4096 || etag != "abc123" {
		t.Fatalf("head info = %d %q %v", size, etag, err)
	}

	if err := client.CopyObject(ctx, "submissions/a.pdf", "books/a.pdf"); err != nil {
		t.Fatal(err)
	}
	if captured.URL.Path != "/books/a.pdf" || captured.Header.Get("x-cos-copy-source") == "" {
		t.Fatalf("copy request = %+v", captured)
	}

	if err := client.DeleteObject(ctx, "books/a.pdf"); err != nil {
		t.Fatal(err)
	}
	if captured.URL.Path != "/books/a.pdf" {
		t.Fatalf("delete path = %q", captured.URL.Path)
	}
}

func TestCosErrorsAndValidation(t *testing.T) {
	ctx := context.Background()
	empty := NewCOSClient("", "", "", "")
	if empty.Configured() {
		t.Fatal("empty client should not be configured")
	}
	if err := empty.PutObject(ctx, "k", "text/plain", []byte("x")); err == nil {
		t.Fatal("put without credentials should fail")
	}
	if _, err := empty.GetObject(ctx, "k"); err == nil {
		t.Fatal("get without credentials should fail")
	}
	if _, err := empty.GetObjectRange(ctx, "k", 0, 1); err == nil {
		t.Fatal("range without credentials should fail")
	}
	if _, _, err := empty.HeadObjectInfo(ctx, "k"); err == nil {
		t.Fatal("head without credentials should fail")
	}
	if err := empty.CopyObject(ctx, "a", "b"); err == nil {
		t.Fatal("copy without credentials should fail")
	}
	if err := empty.DeleteObject(ctx, "k"); err == nil {
		t.Fatal("delete without credentials should fail")
	}
	if _, err := empty.PresignPutURL("k", 10, time.Minute); err == nil {
		t.Fatal("presign without credentials should fail")
	}

	client, _ := newStubCOS(t, func(req *http.Request) (*http.Response, error) {
		return stubResponse(http.StatusForbidden, "<Error>AccessDenied</Error>"), nil
	})
	if _, err := client.GetObjectRange(ctx, "k", 5, 9); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("range error = %v", err)
	}
	if _, err := client.GetObjectRange(ctx, "k", 5, 1); err == nil {
		t.Fatal("invalid range should fail")
	}
	if _, err := client.GetObject(ctx, "k"); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("get error = %v", err)
	}
	if _, _, err := client.HeadObjectInfo(ctx, "k"); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("head error = %v", err)
	}
	if err := client.CopyObject(ctx, "a", "b"); err == nil {
		t.Fatal("copy failure should surface")
	}
	if err := client.PutObject(ctx, "k", "text/plain", []byte("x")); err == nil {
		t.Fatal("put failure should surface")
	}

	network, _ := newStubCOS(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})
	if _, err := network.GetObject(ctx, "k"); err == nil || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("network get error = %v", err)
	}
	if _, err := network.GetObjectRange(ctx, "k", 0, 1); err == nil {
		t.Fatal("network range error should surface")
	}
	if _, _, err := network.HeadObjectInfo(ctx, "k"); err == nil {
		t.Fatal("network head error should surface")
	}
	if err := network.PutObject(ctx, "k", "text/plain", []byte("x")); err == nil {
		t.Fatal("network put error should surface")
	}

	presign, _ := newStubCOS(t, func(*http.Request) (*http.Response, error) { return stubResponse(200, ""), nil })
	if _, err := presign.PresignPutURL("k", 0, time.Minute); err == nil {
		t.Fatal("zero content length should fail")
	}
	url, err := presign.PresignPutURL("books/x.pdf", 10, 48*time.Hour)
	if err != nil || !strings.Contains(url, "q-header-list=content-length%3Bhost") {
		t.Fatalf("presign = %q, %v", url, err)
	}
}

func TestCosHelpers(t *testing.T) {
	if got := cosURIEncode("books/中文 文件.pdf"); !strings.Contains(got, "%2F") || strings.Contains(got, "+") {
		t.Fatalf("cosURIEncode = %q", got)
	}
	if host := NewCOSClient("id", "key", "bucket-1", "ap-shanghai").host(); host != "bucket-1.cos.ap-shanghai.myqcloud.com" {
		t.Fatalf("host = %q", host)
	}
	if host := NewCOSClient("id", "key", "bucket-1", "").host(); host != "bucket-1.cos..myqcloud.com" {
		t.Fatalf("regionless host = %q", host)
	}
	if got := CDNSignedURL("https://dl.xuda.live/", "key", "books/x.pdf", time.Minute); !strings.HasPrefix(got, "https://dl.xuda.live/books/x.pdf?sign=") || !strings.Contains(got, "&t=") {
		t.Fatalf("CDNSignedURL = %q", got)
	}
	if got := CDNSignedURL("https://dl.xuda.live", "key", "/books/x.pdf", time.Minute); !strings.HasPrefix(got, "https://dl.xuda.live/books/x.pdf?") {
		t.Fatalf("CDNSignedURL with slash = %q", got)
	}
}
