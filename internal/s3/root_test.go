package s3

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// folderServer wires a folder-bound bucket into a real HTTP handler so the S3
// layer is exercised end to end: auth, routing, error mapping and the
// filesystem-backed store.
func folderServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, _ := newTestServer(t)
	root := filepath.Join(t.TempDir(), "shared")
	if _, err := s.Store.CreateBucketAt("photos", root); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return s, root
}

func signedRequest(t *testing.T, s *Server, method, path, body string) *http.Request {
	t.Helper()
	key, err := s.Store.ListKeys()
	if err != nil || len(key) == 0 {
		t.Fatalf("list keys: %v", err)
	}
	req, err := http.NewRequest(method, "http://127.0.0.1:9100"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = req.URL.Host
	req.ContentLength = int64(len(body))
	signHeaderRequest(req, key[0], s.Region, sha256Hex([]byte(body)), time.Now())
	return req
}

func doRequest(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, signedRequest(t, s, method, path, body))
	return rec
}

func TestFolderBucketRoundTrip(t *testing.T) {
	s, root := folderServer(t)

	put := doRequest(t, s, "PUT", "/photos/2024/trip.txt", "hello from s3")
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", put.Code, put.Body.String())
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "2024", "trip.txt"))
	if err != nil {
		t.Fatalf("object was not written as a real file: %v", err)
	}
	if string(onDisk) != "hello from s3" {
		t.Fatalf("file content = %q", onDisk)
	}

	get := doRequest(t, s, "GET", "/photos/2024/trip.txt", "")
	if get.Code != http.StatusOK || get.Body.String() != "hello from s3" {
		t.Fatalf("GET = %d %q", get.Code, get.Body.String())
	}

	// An empty body must sign an empty SHA-256, not the PUT payload.
	del := doRequest(t, s, "DELETE", "/photos/2024/trip.txt", "")
	if del.Code != http.StatusNoContent && del.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", del.Code, del.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "2024", "trip.txt")); !os.IsNotExist(err) {
		t.Fatalf("file still exists after delete: %v", err)
	}
}

func TestFolderBucketListDelimiter(t *testing.T) {
	s, _ := folderServer(t)
	for _, k := range []string{"/photos/a/1.txt", "/photos/a/2.txt", "/photos/b/3.txt", "/photos/top.txt"} {
		if rec := doRequest(t, s, "PUT", k, "x"); rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", k, rec.Code, rec.Body.String())
		}
	}
	rec := doRequest(t, s, "GET", "/photos?list-type=2&delimiter=/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("LIST status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		Prefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode listing: %v\n%s", err, rec.Body.String())
	}
	if len(out.Contents) != 1 || out.Contents[0].Key != "top.txt" {
		t.Fatalf("contents = %+v", out.Contents)
	}
	if len(out.Prefixes) != 2 {
		t.Fatalf("prefixes = %+v", out.Prefixes)
	}
}

func TestFolderBucketKeyConflictStatus(t *testing.T) {
	s, _ := folderServer(t)

	if rec := doRequest(t, s, "PUT", "/photos/data", "file"); rec.Code != http.StatusOK {
		t.Fatalf("PUT file = %d %s", rec.Code, rec.Body.String())
	}
	rec := doRequest(t, s, "PUT", "/photos/data/nested.txt", "x")
	if rec.Code != http.StatusConflict {
		t.Fatalf("nested PUT status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "KeyConflict") {
		t.Fatalf("body does not mention KeyConflict: %s", rec.Body.String())
	}
}

func TestFolderBucketRejectsDirectoryKey(t *testing.T) {
	s, _ := folderServer(t)
	rec := doRequest(t, s, "PUT", "/photos/folder/", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT trailing slash = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestFolderBucketPresignDownload(t *testing.T) {
	s, _ := folderServer(t)
	if rec := doRequest(t, s, "PUT", "/photos/p.txt", "presigned body"); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	key, _ := s.Store.ListKeys()
	link, err := Presign("GET", "http://127.0.0.1:9100/", "photos", "p.txt",
		s.Region, key[0].AccessKey, key[0].SecretKey, time.Hour)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("presigned GET = %d, body = %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "presigned body" {
		t.Fatalf("presigned body = %q", body)
	}
}
