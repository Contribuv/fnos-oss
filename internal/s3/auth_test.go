package s3

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"fnososs/internal/store"
)

func newTestServer(t *testing.T) (*Server, store.APIKey) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := st.CreateKey("test")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	s := &Server{
		Store:  st,
		Region: "us-east-1",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return s, key
}

func TestAWSEncode(t *testing.T) {
	cases := map[string]string{
		"abcXYZ019-_.~":   "abcXYZ019-_.~",
		"a b":             "a%20b",
		"a/b":             "a%2Fb",
		"a+b":             "a%2Bb",
		"a=b&c":           "a%3Db%26c",
		"中文":              "%E4%B8%AD%E6%96%87",
		"!*'();:@$,[]?#%": "%21%2A%27%28%29%3B%3A%40%24%2C%5B%5D%3F%23%25",
	}
	for in, want := range cases {
		if got := awsEncode(in); got != want {
			t.Errorf("awsEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalQueryString(t *testing.T) {
	cases := []struct {
		raw     string
		skipSig bool
		want    string
	}{
		{"", false, ""},
		{"b=2&a=1", false, "a=1&b=2"},
		{"a=hello%20world&a=hello", false, "a=hello&a=hello%20world"},
		{"a=hello+world", false, "a=hello%20world"},
		{"X-Amz-Signature=deadbeef&a=1", true, "a=1"},
		{"X-Amz-Signature=deadbeef&a=1", false, "X-Amz-Signature=deadbeef&a=1"},
		{"list-type=2&prefix=dir%2F", false, "list-type=2&prefix=dir%2F"},
		{"empty=&x", false, "empty=&x="},
	}
	for _, c := range cases {
		if got := canonicalQueryString(c.raw, c.skipSig); got != c.want {
			t.Errorf("canonicalQueryString(%q, %v) = %q, want %q", c.raw, c.skipSig, got, c.want)
		}
	}
}

func TestCanonicalHeaders(t *testing.T) {
	r, err := http.NewRequest("GET", "http://example.com:9100/bucket/key", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "example.com:9100"
	r.Header.Set("X-Amz-Date", "20260101T000000Z")
	r.Header.Set("X-Amz-Meta-Spaces", "a   b\t c")
	r.Header.Add("X-Amz-Multi", "one")
	r.Header.Add("X-Amz-Multi", "two")

	got, ok := canonicalHeaders(r, []string{"Host", " x-amz-date", "x-amz-meta-spaces", "x-amz-multi"})
	if !ok {
		t.Fatal("canonicalHeaders returned !ok")
	}
	want := "host:example.com:9100\n" +
		"x-amz-date:20260101T000000Z\n" +
		"x-amz-meta-spaces:a b c\n" +
		"x-amz-multi:one,two\n"
	if got != want {
		t.Errorf("canonicalHeaders:\n got %q\nwant %q", got, want)
	}

	if _, ok := canonicalHeaders(r, []string{"x-amz-missing"}); ok {
		t.Error("canonicalHeaders accepted a missing signed header")
	}
}

func TestCanonicalURLEmpty(t *testing.T) {
	r := &http.Request{URL: &url.URL{}}
	if got := canonicalURI(r); got != "/" {
		t.Errorf("canonicalURI(empty) = %q, want /", got)
	}
}

func TestPresignRoundTrip(t *testing.T) {
	s, key := newTestServer(t)
	keys := []string{
		"plain.txt",
		"dir/with space.txt",
		"中文/文件 (1).txt",
		"a+b=c&d#e?.txt",
	}
	for _, objectKey := range keys {
		link, err := Presign("GET", "http://127.0.0.1:9100/", "my-bucket", objectKey,
			s.Region, key.AccessKey, key.SecretKey, time.Hour)
		if err != nil {
			t.Fatalf("presign %q: %v", objectKey, err)
		}
		req, err := http.NewRequest("GET", link, nil)
		if err != nil {
			t.Fatalf("parse presigned url %q: %v", link, err)
		}
		if want := "/my-bucket/" + objectKey; req.URL.Path != want {
			t.Errorf("presigned path = %q, want %q (url %s)", req.URL.Path, want, link)
		}
		ai, err := s.authenticate(req)
		if err != nil {
			t.Fatalf("authenticate presigned %q: %v (url %s)", objectKey, err, link)
		}
		if ai == nil || !ai.Presigned {
			t.Fatalf("authenticate returned %+v, want presigned auth info", ai)
		}
		if ai.AccessKey != key.AccessKey {
			t.Errorf("access key = %q, want %q", ai.AccessKey, key.AccessKey)
		}
	}
}

func TestPresignRejectsTampering(t *testing.T) {
	s, key := newTestServer(t)
	link, err := Presign("GET", "http://127.0.0.1:9100", "my-bucket", "file.bin",
		s.Region, key.AccessKey, key.SecretKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("X-Amz-Signature", strings.Repeat("0", 64))
	u.RawQuery = q.Encode()
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.authenticate(req); err == nil || err.Error() != errSignatureMismatch.Error() {
		t.Errorf("tampered signature error = %v, want %v", err, errSignatureMismatch)
	}
}

func TestPresignExpired(t *testing.T) {
	s, key := newTestServer(t)
	link, err := Presign("GET", "http://127.0.0.1:9100", "my-bucket", "file.bin",
		s.Region, key.AccessKey, key.SecretKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("X-Amz-Date", time.Now().UTC().Add(-8*24*time.Hour).Format(amzDateFormat))
	u.RawQuery = q.Encode()
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.authenticate(req)
	if err == nil {
		t.Fatal("expired presigned request was accepted")
	}
	apiErr, ok := err.(*APIError)
	if !ok || (apiErr.Code != "RequestTimeTooSkewed" && apiErr.Code != "AccessDenied") {
		t.Errorf("expired presigned error = %v, want time skew or access denied", err)
	}
}

func signHeaderRequest(req *http.Request, key store.APIKey, region, payloadHash string, at time.Time) string {
	amzDate := at.UTC().Format(amzDateFormat)
	date := at.UTC().Format(shortDateFormat)
	scope := date + "/" + region + "/s3/aws4_request"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	headers := strings.Join([]string{
		"host:" + req.Host,
		"x-amz-content-sha256:" + payloadHash,
		"x-amz-date:" + amzDate,
	}, "\n") + "\n"
	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req),
		canonicalQueryString(req.URL.RawQuery, false),
		headers,
		signedHeaders,
		payloadHash,
	}, "\n")
	strToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")
	sig := hex.EncodeToString(hmacSHA256(deriveSigningKey(key.SecretKey, date, region, "s3"), strToSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		key.AccessKey, scope, signedHeaders, sig))
	return amzDate
}

func TestAuthenticateHeader(t *testing.T) {
	s, key := newTestServer(t)
	body := "hello object"
	newReq := func() *http.Request {
		req, err := http.NewRequest("PUT", "http://127.0.0.1:9100/my-bucket/dir/object%20name.txt", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = req.URL.Host
		return req
	}

	req := newReq()
	signHeaderRequest(req, key, s.Region, sha256Hex([]byte(body)), time.Now())
	ai, err := s.authenticate(req)
	if err != nil {
		t.Fatalf("authenticate signed request: %v", err)
	}
	if ai == nil || ai.Presigned || ai.AccessKey != key.AccessKey {
		t.Fatalf("unexpected auth info: %+v", ai)
	}
	if ai.PayloadHash != sha256Hex([]byte(body)) {
		t.Errorf("payload hash = %q", ai.PayloadHash)
	}

	// A modified path must invalidate the signature.
	req = newReq()
	signHeaderRequest(req, key, s.Region, sha256Hex([]byte(body)), time.Now())
	req.URL.Path = "/my-bucket/other.txt"
	if _, err := s.authenticate(req); err == nil {
		t.Error("request with modified path was accepted")
	}

	// A disabled key must not authenticate.
	req = newReq()
	signHeaderRequest(req, key, s.Region, sha256Hex([]byte(body)), time.Now())
	if err := s.Store.SetKeyEnabled(key.AccessKey, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.authenticate(req); err == nil || err.Error() != errInvalidAccessKey.Error() {
		t.Errorf("disabled key error = %v, want %v", err, errInvalidAccessKey)
	}

	// Clock skew is rejected for header-signed requests.
	req = newReq()
	signHeaderRequest(req, key, s.Region, sha256Hex([]byte(body)), time.Now().Add(-time.Hour))
	if err := s.Store.SetKeyEnabled(key.AccessKey, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.authenticate(req); err == nil || err.Error() != errRequestTimeTooSkew.Error() {
		t.Errorf("skewed request error = %v, want %v", err, errRequestTimeTooSkew)
	}
}

func TestAuthenticateAnonymousAndMalformed(t *testing.T) {
	s, _ := newTestServer(t)

	req, _ := http.NewRequest("GET", "http://127.0.0.1:9100/my-bucket/key", nil)
	ai, err := s.authenticate(req)
	if err != nil || ai != nil {
		t.Errorf("anonymous request = (%+v, %v), want (nil, nil)", ai, err)
	}

	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	if _, err := s.authenticate(req); err == nil || err.Error() != errMalformedAuth.Error() {
		t.Errorf("basic auth error = %v, want %v", err, errMalformedAuth)
	}

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=bad, SignedHeaders=host, Signature=x")
	if _, err := s.authenticate(req); err == nil {
		t.Error("malformed credential scope was accepted")
	}
}

func streamingChunkedBody(signingKey []byte, scope, amzDate string, chunks [][]byte) []byte {
	var buf bytes.Buffer
	prev := ""
	sign := func(data []byte) string {
		stringToSign := strings.Join([]string{
			"AWS4-HMAC-SHA256-PAYLOAD",
			amzDate,
			scope,
			prev,
			emptySHA256,
			sha256Hex(data),
		}, "\n")
		prev = hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
		return prev
	}
	for _, data := range chunks {
		fmt.Fprintf(&buf, "%x;chunk-signature=%s\r\n", len(data), sign(data))
		buf.Write(data)
		buf.WriteString("\r\n")
	}
	final := strings.Join([]string{
		"AWS4-HMAC-SHA256-PAYLOAD",
		amzDate,
		scope,
		prev,
		emptySHA256,
		sha256Hex(nil),
	}, "\n")
	sig := hex.EncodeToString(hmacSHA256(signingKey, final))
	fmt.Fprintf(&buf, "0;chunk-signature=%s\r\n\r\n", sig)
	return buf.Bytes()
}

func TestChunkedReader(t *testing.T) {
	_, key := newTestServer(t)
	const amzDate = "20260101T000000Z"
	const scope = "20260101/us-east-1/s3/aws4_request"
	ai := &authInfo{
		SigningKey: deriveSigningKey(key.SecretKey, "20260101", "us-east-1", "s3"),
		Scope:      scope,
		AmzDate:    amzDate,
	}
	chunks := [][]byte{[]byte("hello "), []byte("world"), []byte("!")}
	total := int64(len("hello world!"))
	body := streamingChunkedBody(ai.SigningKey, scope, amzDate, chunks)

	r := newChunkedReader(bytes.NewReader(body), ai, total, true)
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read chunked body: %v", err)
	}
	if string(got) != "hello world!" {
		t.Errorf("chunked body = %q", got)
	}

	// Truncate the declared decoded length: the first chunk must fail.
	r = newChunkedReader(bytes.NewReader(body), ai, 3, true)
	if _, err := io.ReadAll(r); err == nil {
		t.Error("chunk exceeding declared decoded length was accepted")
	}

	// Flip one payload byte: the chunk signature must fail.
	tampered := bytes.Replace(body, []byte("world"), []byte("World"), 1)
	r = newChunkedReader(bytes.NewReader(tampered), ai, total, true)
	if _, err := io.ReadAll(r); err == nil {
		t.Error("tampered chunk payload was accepted")
	}

	// Remove the first chunk signature.
	unsigned := bytes.Replace(body, []byte("chunk-signature="), []byte("chunk-signature=0"), 1)
	r = newChunkedReader(bytes.NewReader(unsigned), ai, total, true)
	if _, err := io.ReadAll(r); err == nil {
		t.Error("missing chunk signature was accepted")
	}
}

func TestDecodeBody(t *testing.T) {
	h := http.Header{}
	h.Set("X-Amz-Decoded-Content-Length", "5")
	r, err := decodeBody(strings.NewReader("1234567890"), nil, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "12345" {
		t.Errorf("limited body = %q, want 12345", got)
	}

	h.Set("X-Amz-Decoded-Content-Length", "-1")
	if _, err := decodeBody(strings.NewReader("x"), nil, h); err == nil {
		t.Error("negative decoded content length was accepted")
	}

	ai := &authInfo{
		Streaming:   true,
		PayloadHash: "STREAMING-AWS4-HMAC-SHA256-PAYLOAD",
		SigningKey:  []byte("key"),
		Scope:       "20260101/us-east-1/s3/aws4_request",
		AmzDate:     "20260101T000000Z",
	}
	h.Set("X-Amz-Decoded-Content-Length", "3")
	r, err = decodeBody(strings.NewReader("3;chunk-signature=x\r\nabc\r\n0\r\n\r\n"), ai, h)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(*chunkedReader); !ok {
		t.Errorf("streaming body type = %T, want *chunkedReader", r)
	}
}
