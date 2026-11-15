package admin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fnososs/internal/store"
)

func TestBackupMetaSnapshot(t *testing.T) {
	a := archiveAdmin(t)
	rec := adminGet(t, a, "/api/backup?scope=meta")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".db") {
		t.Fatalf("content-disposition = %q", cd)
	}
	// bbolt puts the page header first and the meta magic at offset 16.
	body := rec.Body.Bytes()
	if len(body) < 32 || !bytes.Equal(body[16:20], []byte{0xED, 0xDA, 0x0C, 0xED}) {
		t.Fatalf("body is not a bbolt database (%d bytes)", len(body))
	}
}

func TestBackupFullArchive(t *testing.T) {
	a := archiveAdmin(t)
	if _, err := a.Store.CreateBucket("media"); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	writeStoredObject(t, a, "media", "a/b.txt", "hello")

	// A folder-bound bucket must not be copied: its files belong to the user.
	root := filepath.Join(t.TempDir(), "shared")
	if _, err := a.Store.CreateBucketAt("photos", root); err != nil {
		t.Fatalf("create folder bucket: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := adminGet(t, a, "/api/backup?scope=full")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("content-type = %q", ct)
	}

	gz, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	entries := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", h.Name, err)
		}
		entries[h.Name] = string(raw)
	}

	if len(entries["meta.db"]) == 0 {
		t.Fatal("archive is missing meta.db")
	}
	var objectEntry string
	for name := range entries {
		if strings.HasPrefix(name, "objects/media/") {
			objectEntry = name
		}
	}
	if objectEntry == "" {
		t.Fatalf("archive is missing the stored object: %v", keysOf(entries))
	}
	if entries[objectEntry] != "hello" {
		t.Fatalf("object content = %q, want hello", entries[objectEntry])
	}
	for name := range entries {
		if strings.Contains(name, "outside.txt") {
			t.Fatalf("folder-bound file was included in the backup: %s", name)
		}
	}
}

func writeStoredObject(t *testing.T, a *Admin, bucket, key, body string) {
	t.Helper()
	src, err := a.Store.TempFile()
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := src.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	meta := store.Object{
		Key: key, Size: int64(len(body)), ETag: strings.Repeat("a", 32),
		ContentType: "text/plain", ModTime: time.Now(),
	}
	if err := a.Store.SaveObjectFile(bucket, meta, src.Name()); err != nil {
		t.Fatalf("save object: %v", err)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

