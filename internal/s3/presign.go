package s3

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Presign builds an S3 SigV4 query-string URL. endpoint is the public base URL
// of the TCP listener, for example http://192.168.1.10:9100. Path-style
// addressing is always used because that is what this server implements.
func Presign(method, endpoint, bucket, key, region, accessKey, secretKey string, expires time.Duration) (string, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid endpoint")
	}
	now := time.Now().UTC()
	amzDate := now.Format(amzDateFormat)
	date := now.Format(shortDateFormat)
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", date, region)

	escapedKey := strings.Join(escapeSegments(key), "/")
	// Path keeps the decoded key and RawPath the RFC 3986 form. Setting Path to
	// the escaped value would make URL.String() escape the percent signs again.
	u.Path = "/" + bucket + "/" + key
	u.RawPath = "/" + bucket + "/" + escapedKey

	params := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    accessKey + "/" + scope,
		"X-Amz-Date":          amzDate,
		"X-Amz-Expires":       fmt.Sprintf("%d", int64(expires.Seconds())),
		"X-Amz-SignedHeaders": "host",
	}
	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var qparts []string
	for _, k := range keys {
		qparts = append(qparts, awsEncode(k)+"="+awsEncode(params[k]))
	}
	canonicalQuery := strings.Join(qparts, "&")
	u.RawQuery = canonicalQuery

	canonical := strings.Join([]string{
		method,
		u.EscapedPath(),
		canonicalQuery,
		"host:" + u.Host + "\n",
		"host",
		unsignedPayload,
	}, "\n")
	strToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")
	sig := hex.EncodeToString(hmacSHA256(deriveSigningKey(secretKey, date, region, "s3"), strToSign))
	u.RawQuery = canonicalQuery + "&X-Amz-Signature=" + sig
	return u.String(), nil
}

func escapeSegments(key string) []string {
	parts := strings.Split(key, "/")
	for i := range parts {
		parts[i] = awsEncode(parts[i])
	}
	return parts
}
