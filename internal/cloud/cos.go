package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// COSClient talks to a Tencent COS bucket through the XML API: object upload,
// deletion, HEAD metadata and the small ranged reads used to verify direct
// app uploads.
type COSClient struct {
	SecretID  string
	SecretKey string
	Bucket    string
	Region    string
	HTTP      *http.Client
}

func NewCOSClient(secretID, secretKey, bucket, region string) *COSClient {
	return &COSClient{
		SecretID:  secretID,
		SecretKey: secretKey,
		Bucket:    bucket,
		Region:    region,
		// A scanned 80 MB PDF can exceed the old 60 second limit on the
		// Lighthouse instance's public bandwidth.
		HTTP: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *COSClient) Configured() bool {
	return c != nil && c.SecretID != "" && c.SecretKey != "" && c.Bucket != "" && c.Region != ""
}

func (c *COSClient) host() string {
	return fmt.Sprintf("%s.cos.%s.myqcloud.com", c.Bucket, c.Region)
}

func cosURIEncode(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

func cosHMAC(key, message string) []byte {
	mac := hmac.New(sha1.New, []byte(key))
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

// cosSignature returns the hex signature for a COS request.
func cosSignature(secretKey, keyTime, method, path, headerList, headerString string) string {
	httpString := strings.Join([]string{method, path, "", headerString, ""}, "\n")
	httpHash := sha1.Sum([]byte(httpString))
	stringToSign := strings.Join([]string{"sha1", keyTime, hex.EncodeToString(httpHash[:]), ""}, "\n")
	signKey := hex.EncodeToString(cosHMAC(secretKey, keyTime))
	return hex.EncodeToString(cosHMAC(signKey, stringToSign))
}

// cosAuthorization builds the Authorization header value for a signed COS
// request. The COS signing key is the hex digest of HMAC-SHA1(secret, keyTime).
func cosAuthorization(secretID, secretKey, keyTime, method, path, headerList, headerString string) string {
	signature := cosSignature(secretKey, keyTime, method, path, headerList, headerString)
	return "q-sign-algorithm=sha1" +
		"&q-ak=" + secretID +
		"&q-sign-time=" + keyTime +
		"&q-key-time=" + keyTime +
		"&q-header-list=" + headerList +
		"&q-url-param-list=" +
		"&q-signature=" + signature
}

// cosHeaderSignature renders the q-header-list and canonical header string for
// the given headers, both sorted by header name as the COS v5 signature needs.
func cosHeaderSignature(headers map[string]string) (string, string) {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, cosURIEncode(k)+"="+cosURIEncode(headers[k]))
	}
	return strings.Join(keys, ";"), strings.Join(pairs, "&")
}

func (c *COSClient) do(req *http.Request, operation, key string) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("cos %s %s: %s: %s", operation, key, resp.Status, strings.TrimSpace(string(raw)))
	}
	return nil
}

// PutObject uploads body to key with the given content type.
func (c *COSClient) PutObject(ctx context.Context, key, contentType string, body []byte) error {
	if !c.Configured() {
		return fmt.Errorf("cos is not configured")
	}
	path := "/" + strings.TrimPrefix(key, "/")
	host := c.host()
	now := time.Now().Unix()
	keyTime := fmt.Sprintf("%d;%d", now-60, now+3600)

	headerList, headerString := cosHeaderSignature(map[string]string{"content-type": contentType, "host": host})
	authorization := cosAuthorization(c.SecretID, c.SecretKey, keyTime, "put", path, headerList, headerString)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("https://%s%s", host, path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Host = host
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", authorization)
	return c.do(req, "put", key)
}

// PresignPutURL returns a time-limited URL that a client can PUT the object
// to directly, so large uploads do not pass through the control server.
func (c *COSClient) PresignPutURL(key string, contentLength int64, ttl time.Duration) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("cos is not configured")
	}
	if contentLength <= 0 {
		return "", fmt.Errorf("invalid content length %d", contentLength)
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 30 * time.Minute
	}
	path := "/" + strings.TrimPrefix(key, "/")
	host := c.host()
	now := time.Now().Unix()
	keyTime := fmt.Sprintf("%d;%d", now-60, now+int64(ttl.Seconds()))
	headerList, headerString := cosHeaderSignature(map[string]string{
		"content-length": strconv.FormatInt(contentLength, 10),
		"host":           host,
	})
	signature := cosSignature(c.SecretKey, keyTime, "put", path, headerList, headerString)
	params := url.Values{}
	params.Set("q-sign-algorithm", "sha1")
	params.Set("q-ak", c.SecretID)
	params.Set("q-sign-time", keyTime)
	params.Set("q-key-time", keyTime)
	params.Set("q-header-list", headerList)
	params.Set("q-url-param-list", "")
	params.Set("q-signature", signature)
	return fmt.Sprintf("https://%s%s?%s", host, path, params.Encode()), nil
}

// GetObjectRange reads a small byte range of an object. It is used to verify
// the PDF signature of a direct upload without downloading the whole file.
func (c *COSClient) GetObjectRange(ctx context.Context, key string, start, end int64) ([]byte, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("cos is not configured")
	}
	if start < 0 || end < start {
		return nil, fmt.Errorf("invalid range %d-%d", start, end)
	}
	path := "/" + strings.TrimPrefix(key, "/")
	host := c.host()
	now := time.Now().Unix()
	keyTime := fmt.Sprintf("%d;%d", now-60, now+3600)
	rangeValue := fmt.Sprintf("bytes=%d-%d", start, end)
	headerList, headerString := cosHeaderSignature(map[string]string{"host": host, "range": rangeValue})
	authorization := cosAuthorization(c.SecretID, c.SecretKey, keyTime, "get", path, headerList, headerString)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s%s", host, path), nil)
	if err != nil {
		return nil, err
	}
	req.Host = host
	req.Header.Set("Range", rangeValue)
	req.Header.Set("Authorization", authorization)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("cos get %s: %s: %s", key, resp.Status, strings.TrimSpace(string(raw)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, end-start+1))
}

// GetObject starts a signed GET and returns the raw response so callers can
// stream a PDF to a browser with a Content-Disposition header.
func (c *COSClient) GetObject(ctx context.Context, key string) (*http.Response, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("cos is not configured")
	}
	path := "/" + strings.TrimPrefix(key, "/")
	host := c.host()
	now := time.Now().Unix()
	keyTime := fmt.Sprintf("%d;%d", now-60, now+3600)
	headerList, headerString := cosHeaderSignature(map[string]string{"host": host})
	authorization := cosAuthorization(c.SecretID, c.SecretKey, keyTime, "get", path, headerList, headerString)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s%s", host, path), nil)
	if err != nil {
		return nil, err
	}
	req.Host = host
	req.Header.Set("Authorization", authorization)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("cos get %s: %s: %s", key, resp.Status, strings.TrimSpace(string(raw)))
	}
	return resp, nil
}

// HeadObject returns the object's size in bytes without downloading it.
func (c *COSClient) HeadObject(ctx context.Context, key string) (int64, error) {
	if !c.Configured() {
		return 0, fmt.Errorf("cos is not configured")
	}
	path := "/" + strings.TrimPrefix(key, "/")
	host := c.host()
	now := time.Now().Unix()
	keyTime := fmt.Sprintf("%d;%d", now-60, now+3600)

	headerList, headerString := cosHeaderSignature(map[string]string{"host": host})
	authorization := cosAuthorization(c.SecretID, c.SecretKey, keyTime, "head", path, headerList, headerString)

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, fmt.Sprintf("https://%s%s", host, path), nil)
	if err != nil {
		return 0, err
	}
	req.Host = host
	req.Header.Set("Authorization", authorization)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, fmt.Errorf("cos head %s: %s: %s", key, resp.Status, strings.TrimSpace(string(raw)))
	}
	return resp.ContentLength, nil
}

// DeleteObject removes key from the bucket. COS reports success for a key that
// does not exist, so the call is idempotent.
func (c *COSClient) DeleteObject(ctx context.Context, key string) error {
	if !c.Configured() {
		return fmt.Errorf("cos is not configured")
	}
	path := "/" + strings.TrimPrefix(key, "/")
	host := c.host()
	now := time.Now().Unix()
	keyTime := fmt.Sprintf("%d;%d", now-60, now+3600)

	headerList, headerString := cosHeaderSignature(map[string]string{"host": host})
	authorization := cosAuthorization(c.SecretID, c.SecretKey, keyTime, "delete", path, headerList, headerString)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("https://%s%s", host, path), nil)
	if err != nil {
		return err
	}
	req.Host = host
	req.Header.Set("Authorization", authorization)
	return c.do(req, "delete", key)
}
