package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type authInfo struct {
	AccessKey   string
	PayloadHash string
	Streaming   bool
	Presigned   bool
	SigningKey  []byte
	Scope       string
	AmzDate     string
}

const (
	amzDateFormat   = "20060102T150405Z"
	shortDateFormat = "20060102"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	emptySHA256     = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func deriveSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

// awsEncode implements the RFC 3986 encoding used by SigV4 (space becomes %20,
// not +). Slashes are encoded too; callers split paths beforehand.
func awsEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		}
	}
	return b.String()
}

func canonicalQueryString(raw string, skipSignature bool) string {
	if raw == "" {
		return ""
	}
	type pair struct{ k, v string }
	var pairs []pair
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		dk, err := url.QueryUnescape(k)
		if err != nil {
			dk = k
		}
		dv, err := url.QueryUnescape(v)
		if err != nil {
			dv = v
		}
		if skipSignature && dk == "X-Amz-Signature" {
			continue
		}
		pairs = append(pairs, pair{awsEncode(dk), awsEncode(dv)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k == pairs[j].k {
			return pairs[i].v < pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.k)
		b.WriteByte('=')
		b.WriteString(p.v)
	}
	return b.String()
}

func canonicalHeaders(r *http.Request, signed []string) (string, bool) {
	var b strings.Builder
	for _, name := range signed {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		var vals []string
		if name == "host" {
			vals = []string{r.Host}
		} else {
			vals = r.Header.Values(http.CanonicalHeaderKey(name))
			if len(vals) == 0 {
				// Go stores the Authorization header verbatim; nothing else to do.
				vals = r.Header.Values(name)
			}
		}
		if len(vals) == 0 {
			return "", false
		}
		for i := range vals {
			vals[i] = strings.Join(strings.Fields(vals[i]), " ")
		}
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(strings.Join(vals, ","))
		b.WriteByte('\n')
	}
	return b.String(), true
}

func splitCredential(cred string) (access, scope string, ok bool) {
	parts := strings.Split(cred, "/")
	if len(parts) != 5 {
		return "", "", false
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func (s *Server) authenticate(r *http.Request) (*authInfo, error) {
	auth := r.Header.Get("Authorization")
	q := r.URL.Query()
	if auth == "" && q.Get("X-Amz-Signature") == "" {
		return nil, nil // anonymous, caller decides
	}

	var (
		accessKey, scope, signedHeaders, providedSig, amzDate, payloadHash string
		presigned                                                          bool
		expires                                                            int64
	)

	if auth != "" {
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
			return nil, errMalformedAuth
		}
		fields := strings.Split(auth[len("AWS4-HMAC-SHA256 "):], ",")
		values := map[string]string{}
		for _, f := range fields {
			k, v, ok := strings.Cut(strings.TrimSpace(f), "=")
			if !ok {
				continue
			}
			values[k] = v
		}
		var ok bool
		accessKey, scope, ok = splitCredential(values["Credential"])
		if !ok {
			return nil, errMalformedAuth
		}
		signedHeaders = values["SignedHeaders"]
		providedSig = values["Signature"]
		amzDate = r.Header.Get("X-Amz-Date")
		if amzDate == "" {
			amzDate = r.Header.Get("Date")
		}
		payloadHash = r.Header.Get("X-Amz-Content-Sha256")
		if payloadHash == "" {
			payloadHash = unsignedPayload
		}
	} else {
		presigned = true
		var ok bool
		accessKey, scope, ok = splitCredential(q.Get("X-Amz-Credential"))
		if !ok {
			return nil, errMalformedAuth
		}
		signedHeaders = q.Get("X-Amz-SignedHeaders")
		providedSig = q.Get("X-Amz-Signature")
		amzDate = q.Get("X-Amz-Date")
		payloadHash = unsignedPayload
		if v := r.Header.Get("X-Amz-Content-Sha256"); v != "" {
			payloadHash = v
		}
		expires, _ = strconv.ParseInt(q.Get("X-Amz-Expires"), 10, 64)
		if expires < 1 || expires > 604800 {
			return nil, errAccessDenied.withMessage("X-Amz-Expires must be between 1 and 604800 seconds.")
		}
	}

	if providedSig == "" || signedHeaders == "" || amzDate == "" {
		return nil, errMalformedAuth
	}
	reqTime, err := time.Parse(amzDateFormat, amzDate)
	if err != nil {
		// Some clients send RFC1123 in the Date header.
		if t2, err2 := http.ParseTime(amzDate); err2 == nil {
			reqTime = t2
			amzDate = t2.UTC().Format(amzDateFormat)
		} else {
			return nil, errMalformedAuth.withMessage("Invalid X-Amz-Date.")
		}
	}
	now := time.Now().UTC()
	if reqTime.After(now.Add(15*time.Minute)) || now.After(reqTime.Add(15*time.Minute)) {
		if !presigned || now.After(reqTime.Add(time.Duration(expires)*time.Second)) {
			return nil, errRequestTimeTooSkew
		}
	}
	if presigned && now.After(reqTime.Add(time.Duration(expires)*time.Second)) {
		return nil, errAccessDenied.withMessage("Request has expired.")
	}

	access, err := s.Store.GetKey(accessKey)
	if err != nil || !access.Enabled {
		return nil, errInvalidAccessKey
	}
	scopeParts := strings.Split(scope, "/")
	if len(scopeParts) != 4 || scopeParts[3] != "aws4_request" {
		return nil, errMalformedAuth
	}
	date, region, service := scopeParts[0], scopeParts[1], scopeParts[2]
	if service != "s3" {
		return nil, errMalformedAuth
	}
	// The credential scope date must match the request date; a long-lived
	// presigned URL can legitimately cross midnight UTC.
	if date != reqTime.UTC().Format(shortDateFormat) {
		return nil, errRequestTimeTooSkew
	}

	sigKey := deriveSigningKey(access.SecretKey, date, region, service)
	ch, ok := canonicalHeaders(r, strings.Split(signedHeaders, ";"))
	if !ok {
		return nil, errMalformedAuth.withMessage("A signed header is missing from the request.")
	}
	canonical := strings.Join([]string{
		r.Method,
		canonicalURI(r),
		canonicalQueryString(r.URL.RawQuery, presigned),
		ch,
		strings.ToLower(signedHeaders),
		payloadHash,
	}, "\n")
	strToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")
	want := hex.EncodeToString(hmacSHA256(sigKey, strToSign))
	if !hmac.Equal([]byte(strings.ToLower(want)), []byte(strings.ToLower(providedSig))) {
		return nil, errSignatureMismatch
	}

	info := &authInfo{
		AccessKey:   accessKey,
		PayloadHash: payloadHash,
		Presigned:   presigned,
		SigningKey:  sigKey,
		Scope:       scope,
		AmzDate:     amzDate,
	}
	switch payloadHash {
	case "STREAMING-AWS4-HMAC-SHA256-PAYLOAD":
		info.Streaming = true
	case "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER", "STREAMING-UNSIGNED-PAYLOAD-TRAILER":
		info.Streaming = true
	}
	return info, nil
}

func canonicalURI(r *http.Request) string {
	p := r.URL.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}
