package cloud

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
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
