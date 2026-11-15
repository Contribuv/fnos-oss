package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func newRootBucket(t *testing.T, s *Store, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "shared")
	if _, err := s.CreateBucketAt(name, root); err != nil {
		t.Fatalf("create bucket at root: %v", err)
	}
	return root
}

func TestRootBucketWritesRealFiles(t *testing.T) {
	s := newTestStore(t)
	root := newRootBucket(t, s, "photos")

	putObject(t, s, "photos", "2024/trip/a.jpg", "hello")

	got, err := os.ReadFile(filepath.Join(root, "2024", "trip", "a.jpg"))
	if err != nil {
		t.Fatalf("object not written to the folder: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("file content = %q, want hello", got)
	}

	obj, err := s.GetObject("photos", "2024/trip/a.jpg")
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	if obj.Size != 5 || obj.ContentType != "text/plain" {
		t.Fatalf("metadata = %+v", obj)
	}
}

func TestRootBucketSeesExternalFiles(t *testing.T) {
	s := newTestStore(t)
	root := newRootBucket(t, s, "photos")

	// Something the user dropped in through the file manager.
	if err := os.MkdirAll(filepath.Join(root, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inbox", "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.ListObjects("photos", "", "/", "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.CommonPrefixes) != 1 || res.CommonPrefixes[0] != "inbox/" {
		t.Fatalf("common prefixes = %v", res.CommonPrefixes)
	}

	res, err = s.ListObjects("photos", "inbox/", "", "", 100)
	if err != nil {
		t.Fatalf("list prefix: %v", err)
	}
	if keys := listKeys(res); len(keys) != 1 || keys[0] != "inbox/note.txt" {
		t.Fatalf("keys = %v", keys)
	}

	f, obj, err := s.OpenObject("photos", "inbox/note.txt")
	if err != nil {
		t.Fatalf("open external file: %v", err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	if string(body) != "hi" || obj.Size != 2 {
		t.Fatalf("external read = %q size %d", body, obj.Size)
	}
}

func TestRootBucketDeleteAndPrune(t *testing.T) {
	s := newTestStore(t)
	root := newRootBucket(t, s, "photos")
	putObject(t, s, "photos", "a/b/c.txt", "x")

	if _, err := s.DeleteObject("photos", "a/b/c.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c.txt")); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Fatalf("empty folders were not pruned: %v", err)
	}

	existed, err := s.DeleteObject("photos", "a/b/c.txt")
	if err != nil || existed {
		t.Fatalf("second delete = %v, %v", existed, err)
	}
}

func TestRootBucketRejectsTraversal(t *testing.T) {
	s := newTestStore(t)
	newRootBucket(t, s, "photos")

	f, err := s.TempFile()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(f, "x")
	_ = f.Close()

	for _, key := range []string{"../escape", "a/../../b", "/etc/passwd", "a//b"} {
		if err := s.SaveObjectFile("photos", Object{Key: key}, f.Name()); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("key %q: err = %v, want ErrInvalidKey", key, err)
		}
	}
}

func TestRootBucketKeyConflict(t *testing.T) {
	s := newTestStore(t)
	newRootBucket(t, s, "photos")
	putObject(t, s, "photos", "a", "file")

	f, err := s.TempFile()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(f, "y")
	_ = f.Close()
	if err := s.SaveObjectFile("photos", Object{Key: "a/b"}, f.Name()); !errors.Is(err, ErrKeyConflict) {
		t.Fatalf("nested key under a file: err = %v, want ErrKeyConflict", err)
	}
}

func TestRootBucketListPaging(t *testing.T) {
	s := newTestStore(t)
	newRootBucket(t, s, "photos")
	for _, k := range []string{"a.txt", "b.txt", "c.txt"} {
		putObject(t, s, "photos", k, k)
	}

	first, err := s.ListObjects("photos", "", "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !first.IsTruncated || first.NextMarker != "b.txt" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := s.ListObjects("photos", "", "", first.NextMarker, 2)
	if err != nil {
		t.Fatal(err)
	}
	if keys := listKeys(second); len(keys) != 1 || keys[0] != "c.txt" {
		t.Fatalf("second page keys = %v", keys)
	}
}

func TestRootBucketDelimiterNested(t *testing.T) {
	s := newTestStore(t)
	newRootBucket(t, s, "photos")
	for _, k := range []string{"x/1.txt", "x/2.txt", "y/3.txt", "top.txt"} {
		putObject(t, s, "photos", k, k)
	}
	res, err := s.ListObjects("photos", "", "/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string{}, res.CommonPrefixes...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "x/" || got[1] != "y/" {
		t.Fatalf("prefixes = %v", got)
	}
	if keys := listKeys(res); len(keys) != 1 || keys[0] != "top.txt" {
		t.Fatalf("objects = %v", keys)
	}
}

func TestRootPathCannotBeShared(t *testing.T) {
	s := newTestStore(t)
	root := newRootBucket(t, s, "photos")
	if _, err := s.CreateBucketAt("backup", root); !errors.Is(err, ErrRootPathInUse) {
		t.Fatalf("second bucket on same root: err = %v", err)
	}
}

func TestDeleteRootBucketKeepsFolder(t *testing.T) {
	s := newTestStore(t)
	root := newRootBucket(t, s, "photos")
	if err := s.DeleteBucket("photos"); err != nil {
		t.Fatalf("delete empty bucket: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("bucket folder should stay: %v", err)
	}

	if _, err := s.CreateBucketAt("photos", root); err != nil {
		t.Fatalf("recreate bucket: %v", err)
	}
	putObject(t, s, "photos", "keep.txt", "x")
	if err := s.DeleteBucket("photos"); !errors.Is(err, ErrBucketNotEmpty) {
		t.Fatalf("delete non-empty bucket: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Fatalf("object file was removed: %v", err)
	}
}

// Moving a bucket to another folder is how a restored data volume is reconnected
// on a different NAS, where the same files usually sit under a new path.
func TestSetBucketRootRepointsFolder(t *testing.T) {
	s := newTestStore(t)
	first := newRootBucket(t, s, "photos")
	putObject(t, s, "photos", "a.txt", "one")
	if err := os.WriteFile(filepath.Join(first, "old.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := filepath.Join(t.TempDir(), "moved")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "restored.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := s.SetBucketRoot("photos", second)
	if err != nil {
		t.Fatalf("set root: %v", err)
	}
	if b.RootPath != second {
		t.Fatalf("rootPath = %q, want %q", b.RootPath, second)
	}

	res, err := s.ListObjects("photos", "", "", "", 100)
	if err != nil {
		t.Fatalf("list after move: %v", err)
	}
	keys := listKeys(res)
	if len(keys) != 1 || keys[0] != "restored.txt" {
		t.Fatalf("keys after repoint = %v, want [restored.txt]", keys)
	}

	// Switching back to the internal layout must not leave the folder binding.
	b, err = s.SetBucketRoot("photos", "")
	if err != nil {
		t.Fatalf("reset root: %v", err)
	}
	if b.RootPath != "" {
		t.Fatalf("rootPath = %q, want empty", b.RootPath)
	}
}

func TestSetBucketRootRejectsBoundFolder(t *testing.T) {
	s := newTestStore(t)
	first := newRootBucket(t, s, "alpha")
	if _, err := s.CreateBucketAt("beta", filepath.Join(t.TempDir(), "beta")); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := s.SetBucketRoot("beta", first); !errors.Is(err, ErrRootPathInUse) {
		t.Fatalf("repoint onto a bound folder: err = %v, want ErrRootPathInUse", err)
	}
}
