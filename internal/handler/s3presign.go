package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/livekit"
)

// presignS3Get returns a presigned (AWS SigV4) HTTPS URL to GET an object, valid for `expires`.
// Path-style addressing (host/bucket/key) is used so it works for AWS S3 and S3-compatible
// stores (Cloudflare R2, Backblaze B2, MinIO, Wasabi, …) alike — the same bucket Litestream and
// the recordings egress use. No S3 SDK; just the standard signing steps.
func presignS3Get(s3 livekit.S3Config, key string, expires time.Duration, now time.Time) string {
	scheme, host := s3SchemeHost(s3)
	region := s3.Region
	if region == "" {
		region = "us-east-1"
	}
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	scope := dateStamp + "/" + region + "/s3/aws4_request"

	canonURI := "/" + s3EscapePath(s3.Bucket) + "/" + s3EscapePath(key)

	// Query params are signed; sorted, RFC3986-encoded, no X-Amz-Signature yet.
	params := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    s3.AccessKey + "/" + scope,
		"X-Amz-Date":          amzDate,
		"X-Amz-Expires":       strconv.Itoa(int(expires.Seconds())),
		"X-Amz-SignedHeaders": "host",
	}
	canonQuery := s3CanonicalQuery(params)

	canonReq := strings.Join([]string{
		"GET", canonURI, canonQuery,
		"host:" + host + "\n", "host", "UNSIGNED-PAYLOAD",
	}, "\n")
	strToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, s3Hex(s3Sha256([]byte(canonReq))),
	}, "\n")

	kDate := s3HMAC([]byte("AWS4"+s3.Secret), dateStamp)
	kRegion := s3HMAC(kDate, region)
	kService := s3HMAC(kRegion, "s3")
	kSigning := s3HMAC(kService, "aws4_request")
	sig := s3Hex(s3HMAC(kSigning, strToSign))

	return scheme + "://" + host + canonURI + "?" + canonQuery + "&X-Amz-Signature=" + sig
}

// s3SchemeHost resolves the scheme + host for path-style requests. Endpoint set (R2/B2/…) →
// use it; otherwise AWS regional host.
func s3SchemeHost(s3 livekit.S3Config) (scheme, host string) {
	if ep := strings.TrimSpace(s3.Endpoint); ep != "" {
		ep = strings.TrimSuffix(ep, "/")
		if strings.HasPrefix(ep, "http://") {
			return "http", strings.TrimPrefix(ep, "http://")
		}
		return "https", strings.TrimPrefix(strings.TrimPrefix(ep, "https://"), "http://")
	}
	region := s3.Region
	if region == "" || region == "us-east-1" {
		return "https", "s3.amazonaws.com"
	}
	return "https", "s3." + region + ".amazonaws.com"
}

func s3CanonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, s3Escape(k)+"="+s3Escape(params[k]))
	}
	return strings.Join(parts, "&")
}

// s3Escape is RFC3986 encoding (AWS-style): unreserved chars stay, everything else %XX.
func s3Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// s3EscapePath encodes a key, preserving '/' between segments.
func s3EscapePath(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = s3Escape(s)
	}
	return strings.Join(segs, "/")
}

func s3HMAC(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
func s3Sha256(b []byte) []byte { s := sha256.Sum256(b); return s[:] }
func s3Hex(b []byte) string    { return hex.EncodeToString(b) }
