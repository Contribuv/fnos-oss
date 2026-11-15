package admin

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fnososs/internal/config"
	"fnososs/internal/store"
)

// archiveAdmin builds an admin console over a real store so the zip endpoint is
// exercised through the same routing and auth path the browser uses.
func archiveAdmin(t *testing.T) *Admin {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, config.Default(), time.Now(), NewActivity(10))
}

func adminGet(t *testing.T, a *Admin, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-Trim-Username", "admin")
	req.Header.Set("X-Trim-Userid", "1000")
	req.Header.Set("X-Trim-Isadmin", "true")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	return rec
}

func TestArchiveBucketStreamsZip(t *testing.T) {
	a := archiveAdmin(t)
	root := filepath.Join(t.TempDir(), "photos")
	if _, err := a.Store.CreateBucketAt("photos", root); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	files := map[string]string{
		"readme.txt":     "hello",
		"sub/note.txt":   "nested",
		"中文 名称.txt": "中文内容",
	}
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rec := adminGet(t, a, "/api/buckets/photos/archive")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q, want application/zip", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "photos.zip") {
		t.Fatalf("content-disposition = %q, want photos.zip", cd)
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		got[f.Name] = string(raw)
	}
	for name, want := range files {
		if got[name] != want {
			t.Errorf("zip entry %q = %q, want %q (entries: %v)", name, got[name], want, keys(got))
		}
	}
	if len(got) != len(files) {
		t.Errorf("zip has %d entries, want %d: %v", len(got), len(files), keys(got))
	}
}

func TestArchiveBucketHonoursPrefix(t *testing.T) {
	a := archiveAdmin(t)
	root := filepath.Join(t.TempDir(), "photos")
	if _, err := a.Store.CreateBucketAt("photos", root); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	for _, name := range []string{"top.txt", "sub/one.txt", "sub/two.txt"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rec := adminGet(t, a, "/api/buckets/photos/archive?prefix=sub%2F")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "sub.zip") {
		t.Fatalf("content-disposition = %q, want sub.zip", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("zip entries = %d, want 2", len(zr.File))
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "sub/") {
			t.Errorf("entry %q escaped the requested prefix", f.Name)
		}
	}
}

func TestArchiveMissingBucket(t *testing.T) {
	a := archiveAdmin(t)
	rec := adminGet(t, a, "/api/buckets/nope/archive")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
