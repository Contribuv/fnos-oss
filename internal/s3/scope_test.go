package s3

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fnososs/internal/store"
)

func scopedRequest(t *testing.T, s *Server, key store.APIKey, method, path, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://127.0.0.1:9100"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = req.URL.Host
	req.ContentLength = int64(len(body))
	signHeaderRequest(req, key, s.Region, sha256Hex([]byte(body)), time.Now())
	return req
}

func doScoped(t *testing.T, s *Server, key store.APIKey, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, scopedRequest(t, s, key, method, path, body))
	return rec
}

func TestScopedKeyBucketIsolation(t *testing.T) {
	s, _ := newTestServer(t)
	for _, b := range []string{"alpha", "beta"} {
		if _, err := s.Store.CreateBucket(b); err != nil {
			t.Fatalf("create bucket %s: %v", b, err)
		}
	}
	scoped, err := s.Store.CreateKeyScoped("alpha-only", []string{"alpha"}, false)
	if err != nil {
		t.Fatal(err)
	}

	if rec := doScoped(t, s, scoped, "PUT", "/alpha/a.txt", "hello"); rec.Code != http.StatusOK {
		t.Fatalf("PUT alpha = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doScoped(t, s, scoped, "PUT", "/beta/b.txt", "nope"); rec.Code != http.StatusForbidden {
		t.Fatalf("PUT beta = %d, want 403", rec.Code)
	}
	if rec := doScoped(t, s, scoped, "GET", "/beta?list-type=2", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("LIST beta = %d, want 403", rec.Code)
	}

	rec := doScoped(t, s, scoped, "GET", "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ListBuckets = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<Name>alpha</Name>") {
		t.Fatalf("ListBuckets missing allowed bucket: %s", body)
	}
	if strings.Contains(body, "<Name>beta</Name>") {
		t.Fatalf("ListBuckets leaked a bucket outside the key scope: %s", body)
	}
}

func TestReadOnlyKeyRejectsWrites(t *testing.T) {
	s, _ := newTestServer(t)
	if _, err := s.Store.CreateBucket("alpha"); err != nil {
		t.Fatal(err)
	}
	if rec := doRequest(t, s, "PUT", "/alpha/seed.txt", "seed"); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT = %d, body = %s", rec.Code, rec.Body.String())
	}

	ro, err := s.Store.CreateKeyScoped("reader", []string{"alpha"}, true)
	if err != nil {
		t.Fatal(err)
	}

	if rec := doScoped(t, s, ro, "GET", "/alpha/seed.txt", ""); rec.Code != http.StatusOK {
		t.Fatalf("read-only GET = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doScoped(t, s, ro, "PUT", "/alpha/new.txt", "x"); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only PUT = %d, want 403", rec.Code)
	}
	if rec := doScoped(t, s, ro, "DELETE", "/alpha/seed.txt", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only DELETE = %d, want 403", rec.Code)
	}
}

func TestScopedKeyCannotCreateBucket(t *testing.T) {
	s, _ := newTestServer(t)
	scoped, err := s.Store.CreateKeyScoped("scoped", []string{"alpha"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rec := doScoped(t, s, scoped, "PUT", "/gamma", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("scoped key created a bucket: status = %d", rec.Code)
	}
}
