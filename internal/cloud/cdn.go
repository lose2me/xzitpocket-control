package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// cdnSign returns the Tencent CDN type-A signature: md5(authKey + path + t).
func cdnSign(authKey, path string, expires int64) string {
	sum := md5.Sum([]byte(fmt.Sprintf("%s%s%d", authKey, path, expires)))
	return hex.EncodeToString(sum[:])
}

// CDNSignedURL builds a timestamp-signed URL for [path] on [domain], matching
// Tencent CDN type-A authentication: sign = md5(authKey + path + t).
func CDNSignedURL(domain, authKey, path string, ttl time.Duration) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	expires := time.Now().Add(ttl).Unix()
	return fmt.Sprintf("%s%s?sign=%s&t=%d", strings.TrimRight(domain, "/"), path, cdnSign(authKey, path, expires), expires)
}

const cdnAPIHost = "cdn.tencentcloudapi.com"

var cdnAPIClient = &http.Client{Timeout: 30 * time.Second}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

// tc3Authorization builds the Authorization header of a Tencent Cloud API v3
// request (TC3-HMAC-SHA256) for a JSON POST to service at host.
func tc3Authorization(secretID, secretKey, service, host, action, payload string, timestamp int64) string {
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	contentType := "application/json; charset=utf-8"
	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + host + "\n" +
		"x-tc-action:" + strings.ToLower(action) + "\n"
	signedHeaders := "content-type;host;x-tc-action"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + sha256Hex(payload)
	stringToSign := "TC3-HMAC-SHA256\n" + strconv.FormatInt(timestamp, 10) + "\n" +
		date + "/" + service + "/tc3_request\n" + sha256Hex(canonicalRequest)
	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))
	return "TC3-HMAC-SHA256 Credential=" + secretID + "/" + date + "/" + service + "/tc3_request, " +
		"SignedHeaders=" + signedHeaders + ", Signature=" + signature
}

// PurgeCDNCache invalidates the cached copies of urls on Tencent Cloud CDN. The
// URLs must be absolute and must not carry a query string; a URL with nothing
// cached is accepted by the API as a no-op. It does nothing when no credentials
// or URLs are given.
func PurgeCDNCache(ctx context.Context, secretID, secretKey string, urls []string) error {
	if secretID == "" || secretKey == "" || len(urls) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string][]string{"Urls": urls})
	if err != nil {
		return err
	}
	timestamp := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+cdnAPIHost+"/", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", tc3Authorization(secretID, secretKey, "cdn", cdnAPIHost, "PurgeUrlsCache", string(payload), timestamp))
	req.Header.Set("X-TC-Action", "PurgeUrlsCache")
	req.Header.Set("X-TC-Version", "2018-06-06")
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))

	resp, err := cdnAPIClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cdn purge: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		Response struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("cdn purge: decode response: %w", err)
	}
	if parsed.Response.Error != nil {
		return fmt.Errorf("cdn purge: %s: %s", parsed.Response.Error.Code, parsed.Response.Error.Message)
	}
	return nil
}
