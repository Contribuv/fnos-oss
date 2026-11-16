package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func putObject(t *testing.T, s *Store, bucket, key, body string) {
	t.Helper()
	f, err := s.TempFile()
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := io.WriteString(f, body); err != nil {
		_ = f.Close()
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	meta := Object{Key: key, Size: int64(len(body)), ETag: `"` + key + `"`, ContentType: "text/plain"}
	if err := s.SaveObjectFile(bucket, meta, f.Name()); err != nil {
		t.Fatalf("save object %q: %v", key, err)
	}
}

func listKeys(res ListResult) []string {
	out := make([]string, len(res.Objects))
	for i, o := range res.Objects {
		out[i] = o.Key
	}
	return out
}

func TestValidNames(t *testing.T) {
	validBuckets := []string{"abc", "my-bucket", "a1.b2", strings.Repeat("a", 63)}
	for _, name := range validBuckets {
		if !ValidBucketName(name) {
			t.Errorf("ValidBucketName(%q) = false, want true", name)
		}
	}
	invalidBuckets := []string{"", "ab", "ABC", "a..b", "-abc", "abc-", "a_b", strings.Repeat("a", 64)}
	for _, name := range invalidBuckets {
		if ValidBucketName(name) {
			t.Errorf("ValidBucketName(%q) = true, want false", name)
		}
	}

	if !ValidObjectKey(strings.Repeat("k", 1024)) {
		t.Error("1024-byte object key should be valid")
	}
	for _, key := range []string{"", strings.Repeat("k", 1025), "bad\x00key"} {
		if ValidObjectKey(key) {
			t.Errorf("ValidObjectKey(%q) = true, want false", key)
		}
	}
}

func TestListObjectsPagination(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateBucket("list-bucket"); err != nil {
		t.Fatal(err)
	}
	keys := []string{"a.txt", "dir/1.txt", "dir/2.txt", "dir/sub/3.txt", "z.txt"}
	for _, key := range keys {
		putObject(t, s, "list-bucket", key, "data-"+key)
	}

	res, err := s.ListObjects("list-bucket", "", "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := listKeys(res); strings.Join(got, ",") != "a.txt,dir/1.txt" {
		t.Errorf("page 1 objects = %v", got)
	}
	if !res.IsTruncated || res.NextMarker != "dir/1.txt" {
		t.Errorf("page 1 truncated=%v marker=%q", res.IsTruncated, res.NextMarker)
	}

	res, err = s.ListObjects("list-bucket", "", "", res.NextMarker, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := listKeys(res); strings.Join(got, ",") != "dir/2.txt,dir/sub/3.txt" {
		t.Errorf("page 2 objects = %v", got)
	}
	if !res.IsTruncated || res.NextMarker != "dir/sub/3.txt" {
		t.Errorf("page 2 truncated=%v marker=%q", res.IsTruncated, res.NextMarker)
	}

	res, err = s.ListObjects("list-bucket", "", "", res.NextMarker, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := listKeys(res); strings.Join(got, ",") != "z.txt" {
		t.Errorf("page 3 objects = %v", got)
	}
	if res.IsTruncated {
		t.Error("page 3 should not be truncated")
	}
}

func TestListObjectsDelimiterPagination(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateBucket("group-bucket"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a.txt", "dir/1.txt", "dir/2.txt", "dir/sub/3.txt", "z.txt"} {
		putObject(t, s, "group-bucket", key, "x")
	}

	res, err := s.ListObjects("group-bucket", "", "/", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := listKeys(res); strings.Join(got, ",") != "a.txt" {
		t.Errorf("grouped page 1 objects = %v", got)
	}
	if len(res.CommonPrefixes) != 1 || res.CommonPrefixes[0] != "dir/" {
		t.Errorf("grouped page 1 prefixes = %v", res.CommonPrefixes)
	}
	if !res.IsTruncated || res.NextMarker != "dir/" {
		t.Fatalf("grouped page 1 truncated=%v marker=%q", res.IsTruncated, res.NextMarker)
	}

	res, err = s.ListObjects("group-bucket", "", "/", res.NextMarker, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := listKeys(res); strings.Join(got, ",") != "z.txt" {
		t.Errorf("grouped page 2 objects = %v, want [z.txt]", got)
	}
	if len(res.CommonPrefixes) != 0 {
		t.Errorf("grouped page 2 repeated prefixes: %v", res.CommonPrefixes)
	}
	if res.IsTruncated {
		t.Error("grouped page 2 should not be truncated")
	}

	// Prefix-scoped listing groups "dir/sub/3.txt" under "dir/sub/".
	res, err = s.ListObjects("group-bucket", "dir/", "/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := listKeys(res); strings.Join(got, ",") != "dir/1.txt,dir/2.txt" {
		t.Errorf("prefixed objects = %v", got)
	}
	if len(res.CommonPrefixes) != 1 || res.CommonPrefixes[0] != "dir/sub/" {
		t.Errorf("prefixed prefixes = %v", res.CommonPrefixes)
	}
	if res.KeyCount != 3 {
		t.Errorf("prefixed KeyCount = %d, want 3", res.KeyCount)
	}
}

func TestMultipartComplete(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateBucket("mpu-bucket"); err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUpload(Upload{Bucket: "mpu-bucket", Key: "big/obj.bin", ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	chunks := []string{"hello ", "multipart ", "world"}
	var parts []Part
	for i, chunk := range chunks {
		f, err := s.TempFile()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, chunk); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		p := Part{
			Number:  i + 1,
			Size:    int64(len(chunk)),
			ETag:    fmt.Sprintf(`"part-%d"`, i+1),
			ModTime: time.Now().UTC(),
		}
		if err := s.PutPart(u.UploadID, p, f.Name()); err != nil {
			t.Fatalf("put part %d: %v", p.Number, err)
		}
		parts = append(parts, p)
	}

	listed, err := s.ListParts(u.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(parts) {
		t.Fatalf("ListParts returned %d parts, want %d", len(listed), len(parts))
	}
	for i := range listed {
		if listed[i].Number != parts[i].Number {
			t.Errorf("part %d number = %d", i, listed[i].Number)
		}
	}

	meta, err := s.CompleteUpload(u, parts, `"final-etag"`)
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	if meta.Size != int64(len("hello multipart world")) {
		t.Errorf("object size = %d", meta.Size)
	}
	f, obj, err := s.OpenObject("mpu-bucket", "big/obj.bin")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello multipart world" {
		t.Errorf("object body = %q", data)
	}
	if obj.ContentType != "text/plain" || obj.ETag != `"final-etag"` {
		t.Errorf("object metadata = %+v", obj)
	}

	if _, err := s.GetUpload(u.UploadID); !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("upload still exists after completion: %v", err)
	}
	if _, err := os.Stat(s.PartPath(u.UploadID, 1)); !os.IsNotExist(err) {
		t.Errorf("part file still exists after completion: %v", err)
	}

	if err := s.DeleteBucket("mpu-bucket"); !errors.Is(err, ErrBucketNotEmpty) {
		t.Errorf("DeleteBucket(non-empty) = %v, want ErrBucketNotEmpty", err)
	}
}

func TestObjectAndBucketLifecycle(t *testing.T) {
	s := newTestStore(t)
	b, err := s.CreateBucket("life-bucket")
	if err != nil {
		t.Fatal(err)
	}
	if b.Policy != "private" {
		t.Errorf("new bucket policy = %q", b.Policy)
	}
	if err := s.SetBucketPolicy("life-bucket", "public-read"); err != nil {
		t.Fatal(err)
	}
	b, err = s.GetBucket("life-bucket")
	if err != nil || b.Policy != "public-read" {
		t.Fatalf("GetBucket = %+v, %v", b, err)
	}

	putObject(t, s, "life-bucket", "k/one.txt", "one")
	putObject(t, s, "life-bucket", "k/two.txt", "two-two")
	if n, size := s.BucketUsage("life-bucket"); n != 2 || size != int64(len("one")+len("two-two")) {
		t.Errorf("BucketUsage = (%d, %d)", n, size)
	}
	existed, err := s.DeleteObject("life-bucket", "k/one.txt")
	if err != nil || !existed {
		t.Fatalf("DeleteObject = (%v, %v)", existed, err)
	}
	existed, err = s.DeleteObject("life-bucket", "k/one.txt")
	if err != nil || existed {
		t.Fatalf("second DeleteObject = (%v, %v), want (false, nil)", existed, err)
	}
	if err := s.DeleteBucket("life-bucket"); !errors.Is(err, ErrBucketNotEmpty) {
		t.Errorf("DeleteBucket(non-empty) = %v", err)
	}
	if _, err := s.DeleteObject("life-bucket", "k/two.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket("life-bucket"); err != nil {
		t.Fatalf("DeleteBucket(empty) = %v", err)
	}
	if _, err := s.GetBucket("life-bucket"); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("GetBucket after delete = %v", err)
	}
}

func TestKeysAndSettings(t *testing.T) {
	s := newTestStore(t)

	k, created, err := s.EnsureDefaultKey()
	if err != nil || !created {
		t.Fatalf("EnsureDefaultKey = (%+v, %v, %v)", k, created, err)
	}
	if !strings.HasPrefix(k.AccessKey, "AKIA") || !k.Enabled {
		t.Errorf("default key = %+v", k)
	}
	if _, created, err := s.EnsureDefaultKey(); err != nil || created {
		t.Fatalf("second EnsureDefaultKey created=%v err=%v", created, err)
	}

	if err := s.SetKeyEnabled(k.AccessKey, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetKey(k.AccessKey)
	if err != nil || got.Enabled {
		t.Errorf("GetKey after disable = (%+v, %v)", got, err)
	}
	if err := s.DeleteKey(k.AccessKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKey(k.AccessKey); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("GetKey after delete = %v", err)
	}

	settings, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.CORSOrigins) != 1 || settings.CORSOrigins[0] != "*" {
		t.Errorf("default settings = %+v", settings)
	}
	settings.CORSOrigins = []string{"https://example.com"}
	settings.MaxUploadSize = 1024
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	again, err := s.Settings()
	if err != nil || len(again.CORSOrigins) != 1 || again.MaxUploadSize != 1024 {
		t.Errorf("saved settings = %+v, %v", again, err)
	}
}

func TestKeyScopeRoundTrip(t *testing.T) {
	s := newTestStore(t)
	k, err := s.CreateKeyScoped("scoped", []string{"b", "a", "a", " "}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Buckets) != 2 || k.Buckets[0] != "a" || k.Buckets[1] != "b" {
		t.Fatalf("normalized scope = %v", k.Buckets)
	}
	if err := s.SetKeyScope(k.AccessKey, []string{"photos"}, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetKey(k.AccessKey)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReadOnly || len(got.Buckets) != 1 || got.Buckets[0] != "photos" {
		t.Fatalf("scope after update = %+v", got)
	}
	if err := s.SetKeyScope(k.AccessKey, nil, false); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetKey(k.AccessKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 0 || got.ReadOnly {
		t.Fatalf("scope after clear = %+v", got)
	}
	if !got.AllowsBucket("any-bucket") {
		t.Fatal("cleared scope should allow every bucket")
	}
}
