package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	ErrBucketNotFound    = errors.New("bucket not found")
	ErrBucketNotEmpty    = errors.New("bucket not empty")
	ErrObjectNotFound    = errors.New("object not found")
	ErrUploadNotFound    = errors.New("upload not found")
	ErrInvalidBucketName = errors.New("invalid bucket name")
	ErrInvalidKey        = errors.New("invalid object key")
	ErrKeyNotFound       = errors.New("access key not found")
)

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

var (
	bktBuckets = []byte("buckets")
	bktObjects = []byte("objects")
	bktUploads = []byte("uploads")
	bktParts   = []byte("parts")
	bktKeys    = []byte("keys")
	bktMeta    = []byte("meta")
)

// Store keeps object metadata in bbolt and object bytes on the filesystem.
// Both live under DataDir so a fnOS "var" volume backup captures everything.
type Store struct {
	db      *bolt.DB
	dataDir string

	statsMu  sync.Mutex
	statsAt  time.Time
	statsVal Stats

	usageMu sync.Mutex
	usage   map[string]usageEntry
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	for _, d := range []string{"objects", "uploads", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := bolt.Open(filepath.Join(dataDir, "meta.db"), 0o600, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, dataDir: dataDir}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bktBuckets, bktObjects, bktUploads, bktParts, bktKeys, bktMeta} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DataDir() string { return s.dataDir }

// WriteSnapshot streams a consistent copy of the metadata database. bbolt
// copies from a read transaction, so a running server can keep writing while
// the backup is produced; copying meta.db as a plain file could capture a page
// in the middle of an update.
func (s *Store) WriteSnapshot(w io.Writer) error {
	return s.db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(w)
		return err
	})
}

func ValidBucketName(name string) bool {
	return bucketNameRe.MatchString(name) && !strings.Contains(name, "..")
}

func ValidObjectKey(key string) bool {
	return key != "" && len(key) <= 1024 && !strings.Contains(key, "\x00")
}

func objKey(bucket, key string) []byte {
	return []byte(bucket + "\x00" + key)
}

func decode[T any](raw []byte) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, errors.New("empty record")
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

// ---------------------------------------------------------------- buckets

func (s *Store) CreateBucket(name string) (Bucket, error) {
	return s.CreateBucketAt(name, "")
}

// CreateBucketAt creates a bucket. An empty rootPath keeps the bucket inside the
// app data volume; otherwise object keys are written as real files below it.
func (s *Store) CreateBucketAt(name, rootPath string) (Bucket, error) {
	if !ValidBucketName(name) {
		return Bucket{}, ErrInvalidBucketName
	}
	root, err := normalizeRoot(rootPath)
	if err != nil {
		return Bucket{}, err
	}
	if root != "" {
		if err := s.ensureRootUsable(name, root); err != nil {
			return Bucket{}, err
		}
	}
	b := Bucket{Name: name, Created: time.Now().UTC(), Policy: "private", RootPath: root}
	err = s.db.Update(func(tx *bolt.Tx) error {
		bk := tx.Bucket(bktBuckets)
		if bk.Get([]byte(name)) != nil {
			return fmt.Errorf("bucket exists")
		}
		raw, _ := json.Marshal(b)
		return bk.Put([]byte(name), raw)
	})
	if err != nil {
		return Bucket{}, err
	}
	if b.RootPath == "" {
		if err := os.MkdirAll(filepath.Join(s.dataDir, "objects", name), 0o755); err != nil {
			return Bucket{}, err
		}
	}
	return b, nil
}

// ensureRootUsable rejects folders already bound to another bucket and verifies
// the app user can actually write there (fnOS grants the ACL at pick time).
func (s *Store) ensureRootUsable(name, root string) error {
	buckets, err := s.ListBuckets()
	if err != nil {
		return err
	}
	for _, other := range buckets {
		if other.Name == name || other.RootPath == "" {
			continue
		}
		if samePath(other.RootPath, root) {
			return ErrRootPathInUse
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRootPath, err)
	}
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return ErrInvalidRootPath
	}
	probe, err := os.CreateTemp(root, ".fnos-oss-probe-*")
	if err != nil {
		return fmt.Errorf("%w: 应用没有该目录的写入权限", ErrInvalidRootPath)
	}
	probeName := probe.Name()
	probe.Close()
	_ = os.Remove(probeName)
	return nil
}

func (s *Store) GetBucket(name string) (Bucket, error) {
	var b Bucket
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktBuckets).Get([]byte(name))
		if raw == nil {
			return ErrBucketNotFound
		}
		var err error
		b, err = decode[Bucket](raw)
		return err
	})
	return b, err
}

func (s *Store) ListBuckets() ([]Bucket, error) {
	var out []Bucket
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktBuckets).ForEach(func(k, v []byte) error {
			b, err := decode[Bucket](v)
			if err == nil {
				out = append(out, b)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

func (s *Store) SetBucketPolicy(name, policy string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktBuckets)
		raw := root.Get([]byte(name))
		if raw == nil {
			return ErrBucketNotFound
		}
		b, err := decode[Bucket](raw)
		if err != nil {
			return err
		}
		b.Policy = policy
		raw, _ = json.Marshal(b)
		return root.Put([]byte(name), raw)
	})
}

// SetBucketRoot re-points a bucket at another folder without touching any
// files. This is what makes a copied data volume usable after a move to a
// different NAS, where the same folder usually lives under another path.
func (s *Store) SetBucketRoot(name, rootPath string) (Bucket, error) {
	root, err := normalizeRoot(rootPath)
	if err != nil {
		return Bucket{}, err
	}
	b, err := s.GetBucket(name)
	if err != nil {
		return Bucket{}, err
	}
	if root != "" {
		if err := s.ensureRootUsable(name, root); err != nil {
			return Bucket{}, err
		}
	}
	b.RootPath = root
	err = s.db.Update(func(tx *bolt.Tx) error {
		raw, _ := json.Marshal(b)
		return tx.Bucket(bktBuckets).Put([]byte(name), raw)
	})
	if err != nil {
		return Bucket{}, err
	}
	if root == "" {
		if err := os.MkdirAll(filepath.Join(s.dataDir, "objects", name), 0o755); err != nil {
			return Bucket{}, err
		}
	}
	// Cached usage belongs to the previous location and would be wrong now.
	s.usageMu.Lock()
	delete(s.usage, name)
	s.usageMu.Unlock()
	return b, nil
}

func (s *Store) DeleteBucket(name string) error {
	b, err := s.GetBucket(name)
	if err != nil {
		return err
	}
	// A bucket bound to a folder only counts the files inside that folder, and
	// the folder itself is never deleted: it belongs to the user.
	if b.RootPath != "" {
		empty, err := dirIsEmpty(b.RootPath)
		if err != nil {
			return err
		}
		if !empty {
			return ErrBucketNotEmpty
		}
	} else {
		prefix := []byte(name + "\x00")
		hasObjects := false
		err := s.db.View(func(tx *bolt.Tx) error {
			c := tx.Bucket(bktObjects).Cursor()
			k, _ := c.Seek(prefix)
			if k != nil && strings.HasPrefix(string(k), name+"\x00") {
				hasObjects = true
			}
			return nil
		})
		if err != nil {
			return err
		}
		if hasObjects {
			return ErrBucketNotEmpty
		}
	}
	// Abort any dangling multipart uploads for this bucket.
	uploads, _ := s.ListUploads(name)
	for _, u := range uploads {
		_ = s.AbortUpload(u.UploadID)
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bktBuckets).Delete([]byte(name)); err != nil {
			return err
		}
		// Drop index records that may outlive files deleted in the file manager.
		root := tx.Bucket(bktObjects)
		c := root.Cursor()
		prefix := []byte(name + "\x00")
		for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
			if err := c.Delete(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if b.RootPath == "" {
		_ = os.RemoveAll(filepath.Join(s.dataDir, "objects", name))
	}
	s.invalidateStats()
	return nil
}

// ---------------------------------------------------------------- objects

func (s *Store) blobPath(bucket, key string) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(s.dataDir, "objects", bucket, h[:2], h[2:4], h)
}

func (s *Store) BlobPath(bucket, key string) string { return s.blobPath(bucket, key) }

// BucketUsage scans the object index for a bucket. Bucket counts are small
// enough on a NAS that this is cheaper than maintaining separate counters that
// can drift after crashes. Buckets bound to a folder are counted from disk and
// cached briefly, because a user's folder can hold a lot of files.
func (s *Store) BucketUsage(name string) (objects int64, size int64) {
	b, err := s.GetBucket(name)
	if err != nil {
		return 0, 0
	}
	if b.RootPath != "" {
		if objects, size, ok := s.cachedUsage(name); ok {
			return objects, size
		}
		objects, size = s.usageFromRoot(b.RootPath)
		s.cacheUsage(name, objects, size)
		return objects, size
	}
	prefix := []byte(name + "\x00")
	_ = s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bktObjects).Cursor()
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, v = c.Next() {
			o, err := decode[Object](v)
			if err == nil {
				objects++
				size += o.Size
			}
		}
		return nil
	})
	return objects, size
}

type usageEntry struct {
	at      time.Time
	objects int64
	size    int64
}

func (s *Store) cachedUsage(bucket string) (int64, int64, bool) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	e, ok := s.usage[bucket]
	if !ok || time.Since(e.at) > time.Minute {
		return 0, 0, false
	}
	return e.objects, e.size, true
}

func (s *Store) cacheUsage(bucket string, objects, size int64) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.usage == nil {
		s.usage = map[string]usageEntry{}
	}
	s.usage[bucket] = usageEntry{at: time.Now(), objects: objects, size: size}
}

func (s *Store) TempFile() (*os.File, error) {
	return os.CreateTemp(filepath.Join(s.dataDir, "tmp"), "oss-*")
}

// SaveObjectFile atomically installs src as the object payload and writes its
// metadata. The caller owns src and may delete it after this returns.
func (s *Store) SaveObjectFile(bucket string, meta Object, src string) error {
	b, err := s.GetBucket(bucket)
	if err != nil {
		return err
	}
	if b.RootPath != "" {
		return s.saveObjectToRoot(b, meta, src)
	}
	return s.saveObjectBlob(bucket, meta, src)
}

func (s *Store) saveObjectBlob(bucket string, meta Object, src string) error {
	dst := s.blobPath(bucket, meta.Key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		// Cross-device fallback (tmp and objects normally share a volume).
		if err := copyFile(src, dst); err != nil {
			return err
		}
		_ = os.Remove(src)
	}
	if meta.ModTime.IsZero() {
		meta.ModTime = time.Now().UTC()
	}
	if meta.StorageClass == "" {
		meta.StorageClass = "STANDARD"
	}
	raw, _ := json.Marshal(meta)
	err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bktObjects).Put(objKey(bucket, meta.Key), raw)
	})
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	s.invalidateStats()
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := out.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(dst)
	}
	return cerr
}

func (s *Store) GetObject(bucket, key string) (Object, error) {
	b, err := s.GetBucket(bucket)
	if err != nil {
		return Object{}, err
	}
	if b.RootPath != "" {
		return s.getObjectFromRoot(b, key)
	}
	return s.getObjectBlob(bucket, key)
}

func (s *Store) getObjectBlob(bucket, key string) (Object, error) {
	var o Object
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktObjects).Get(objKey(bucket, key))
		if raw == nil {
			return ErrObjectNotFound
		}
		var err error
		o, err = decode[Object](raw)
		return err
	})
	return o, err
}

func (s *Store) OpenObject(bucket, key string) (*os.File, Object, error) {
	o, err := s.GetObject(bucket, key)
	if err != nil {
		return nil, o, err
	}
	b, err := s.GetBucket(bucket)
	if err != nil {
		return nil, o, err
	}
	p := s.blobPath(bucket, key)
	if b.RootPath != "" {
		p, err = fsObjectPath(b.RootPath, key)
		if err != nil {
			return nil, o, err
		}
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, o, ErrObjectNotFound
		}
		return nil, o, err
	}
	return f, o, nil
}

func (s *Store) DeleteObject(bucket, key string) (bool, error) {
	b, err := s.GetBucket(bucket)
	if err != nil {
		return false, err
	}
	if b.RootPath != "" {
		return s.deleteFromRoot(b, key)
	}
	existed := false
	err = s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktObjects)
		k := objKey(bucket, key)
		if root.Get(k) != nil {
			existed = true
		}
		return root.Delete(k)
	})
	if err != nil {
		return existed, err
	}
	if existed {
		_ = os.Remove(s.blobPath(bucket, key))
		s.invalidateStats()
	}
	return existed, nil
}

func (s *Store) ListObjects(bucket, prefix, delimiter, after string, max int) (ListResult, error) {
	b, err := s.GetBucket(bucket)
	if err != nil {
		return ListResult{}, err
	}
	if max <= 0 || max > 1000 {
		max = 1000
	}
	if b.RootPath != "" {
		return s.listObjectsFromRoot(b, prefix, delimiter, after, max)
	}
	return s.listObjectsIndex(bucket, prefix, delimiter, after, max)
}

func (s *Store) listObjectsIndex(bucket, prefix, delimiter, after string, max int) (ListResult, error) {
	res := ListResult{}
	seenPrefix := map[string]bool{}
	base := []byte(bucket + "\x00")
	full := append(append([]byte{}, base...), []byte(prefix)...)
	count := 0
	lastMarker := ""
	// When the previous page ended on a common prefix, the marker is that
	// prefix itself; every key below it belongs to the already-returned group.
	skipPrefix := ""
	if delimiter != "" && after != "" && strings.HasSuffix(after, delimiter) {
		skipPrefix = after
	}

	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bktObjects).Cursor()
		for k, v := c.Seek(full); k != nil && strings.HasPrefix(string(k), string(base)); k, v = c.Next() {
			key := string(k[len(base):])
			if !strings.HasPrefix(key, prefix) {
				break
			}
			if after != "" && key <= after {
				continue
			}
			if skipPrefix != "" && strings.HasPrefix(key, skipPrefix) {
				continue
			}
			if count >= max {
				res.IsTruncated = true
				res.NextMarker = lastMarker
				break
			}
			if delimiter != "" {
				rest := key[len(prefix):]
				if idx := strings.Index(rest, delimiter); idx >= 0 {
					cp := prefix + rest[:idx+len(delimiter)]
					if !seenPrefix[cp] {
						seenPrefix[cp] = true
						res.CommonPrefixes = append(res.CommonPrefixes, cp)
						count++
						lastMarker = cp
					}
					continue
				}
			}
			o, err := decode[Object](v)
			if err != nil {
				continue
			}
			res.Objects = append(res.Objects, o)
			count++
			lastMarker = key
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	sort.Strings(res.CommonPrefixes)
	res.KeyCount = len(res.Objects) + len(res.CommonPrefixes)
	return res, nil
}

// ---------------------------------------------------------------- multipart

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) CreateUpload(u Upload) (Upload, error) {
	if _, err := s.GetBucket(u.Bucket); err != nil {
		return Upload{}, err
	}
	u.UploadID = randomID(20)
	u.Created = time.Now().UTC()
	if u.StorageClass == "" {
		u.StorageClass = "STANDARD"
	}
	raw, _ := json.Marshal(u)
	err := s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bktUploads).Put([]byte(u.UploadID), raw); err != nil {
			return err
		}
		return tx.Bucket(bktMeta).Put([]byte("upload:"+u.UploadID), []byte(u.Bucket))
	})
	if err != nil {
		return Upload{}, err
	}
	_ = os.MkdirAll(filepath.Join(s.dataDir, "uploads", u.UploadID), 0o755)
	return u, nil
}

func (s *Store) GetUpload(id string) (Upload, error) {
	var u Upload
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktUploads).Get([]byte(id))
		if raw == nil {
			return ErrUploadNotFound
		}
		var err error
		u, err = decode[Upload](raw)
		return err
	})
	return u, err
}

func (s *Store) ListUploads(bucket string) ([]Upload, error) {
	var out []Upload
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktUploads).ForEach(func(k, v []byte) error {
			u, err := decode[Upload](v)
			if err == nil && (bucket == "" || u.Bucket == bucket) {
				out = append(out, u)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, err
}

func (s *Store) PutPart(id string, part Part, src string) error {
	if _, err := s.GetUpload(id); err != nil {
		return err
	}
	dst := filepath.Join(s.dataDir, "uploads", id, fmt.Sprintf("%05d", part.Number))
	if err := os.Rename(src, dst); err != nil {
		if err := copyFile(src, dst); err != nil {
			return err
		}
		_ = os.Remove(src)
	}
	raw, _ := json.Marshal(part)
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bktParts).Put([]byte(fmt.Sprintf("%s\x00%05d", id, part.Number)), raw)
	})
}

func (s *Store) ListParts(id string) ([]Part, error) {
	if _, err := s.GetUpload(id); err != nil {
		return nil, err
	}
	var out []Part
	prefix := []byte(id + "\x00")
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bktParts).Cursor()
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), id+"\x00"); k, v = c.Next() {
			p, err := decode[Part](v)
			if err == nil {
				out = append(out, p)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, err
}

func (s *Store) PartPath(id string, number int) string {
	return filepath.Join(s.dataDir, "uploads", id, fmt.Sprintf("%05d", number))
}

func (s *Store) AbortUpload(id string) error {
	if _, err := s.GetUpload(id); err != nil {
		return err
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bktUploads).Delete([]byte(id)); err != nil {
			return err
		}
		_ = tx.Bucket(bktMeta).Delete([]byte("upload:" + id))
		c := tx.Bucket(bktParts).Cursor()
		prefix := []byte(id + "\x00")
		for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), id+"\x00"); k, _ = c.Next() {
			if err := c.Delete(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(s.dataDir, "uploads", id))
	s.invalidateStats()
	return nil
}

// CompleteUpload concatenates the requested parts in order and stores the
// result as a normal object. It returns the resulting metadata.
func (s *Store) CompleteUpload(u Upload, parts []Part, etag string) (Object, error) {
	tmp, err := s.TempFile()
	if err != nil {
		return Object{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	for _, p := range parts {
		f, err := os.Open(s.PartPath(u.UploadID, p.Number))
		if err != nil {
			tmp.Close()
			return Object{}, ErrUploadNotFound
		}
		_, err = io.Copy(tmp, f)
		f.Close()
		if err != nil {
			tmp.Close()
			return Object{}, err
		}
	}
	if err := tmp.Close(); err != nil {
		return Object{}, err
	}
	meta := Object{
		Key:          u.Key,
		Size:         sumParts(parts),
		ETag:         etag,
		ModTime:      time.Now().UTC(),
		ContentType:  u.ContentType,
		StorageClass: u.StorageClass,
		Headers:      u.Headers,
		UserMeta:     u.UserMeta,
	}
	if meta.ContentType == "" {
		meta.ContentType = "application/octet-stream"
	}
	if err := s.SaveObjectFile(u.Bucket, meta, tmpName); err != nil {
		return Object{}, err
	}
	_ = s.AbortUpload(u.UploadID)
	return meta, nil
}

func sumParts(parts []Part) int64 {
	var n int64
	for _, p := range parts {
		n += p.Size
	}
	return n
}

// ---------------------------------------------------------------- keys

func (s *Store) CreateKey(label string) (APIKey, error) {
	if label == "" {
		label = "default"
	}
	k := APIKey{
		AccessKey: "AKIA" + strings.ToUpper(randomID(8)),
		SecretKey: randomID(20),
		Label:     label,
		Created:   time.Now().UTC(),
		Enabled:   true,
	}
	raw, _ := json.Marshal(k)
	err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bktKeys).Put([]byte(k.AccessKey), raw)
	})
	return k, err
}

func (s *Store) ListKeys() ([]APIKey, error) {
	var out []APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktKeys).ForEach(func(k, v []byte) error {
			key, err := decode[APIKey](v)
			if err == nil {
				out = append(out, key)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, err
}

func (s *Store) GetKey(access string) (APIKey, error) {
	var k APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktKeys).Get([]byte(access))
		if raw == nil {
			return ErrKeyNotFound
		}
		var err error
		k, err = decode[APIKey](raw)
		return err
	})
	return k, err
}

func (s *Store) SetKeyEnabled(access string, enabled bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktKeys)
		raw := root.Get([]byte(access))
		if raw == nil {
			return ErrKeyNotFound
		}
		k, err := decode[APIKey](raw)
		if err != nil {
			return err
		}
		k.Enabled = enabled
		raw, _ = json.Marshal(k)
		return root.Put([]byte(access), raw)
	})
}

func (s *Store) TouchKey(access string) {
	_ = s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktKeys)
		raw := root.Get([]byte(access))
		if raw == nil {
			return nil
		}
		k, err := decode[APIKey](raw)
		if err != nil {
			return nil
		}
		now := time.Now().UTC()
		k.LastUsed = &now
		raw, _ = json.Marshal(k)
		return root.Put([]byte(access), raw)
	})
}

func (s *Store) DeleteKey(access string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bktKeys).Delete([]byte(access))
	})
}

func (s *Store) EnsureDefaultKey() (APIKey, bool, error) {
	keys, err := s.ListKeys()
	if err != nil {
		return APIKey{}, false, err
	}
	if len(keys) > 0 {
		return keys[0], false, nil
	}
	k, err := s.CreateKey("default")
	return k, true, err
}

// ---------------------------------------------------------------- settings

func (s *Store) Settings() (Settings, error) {
	out := DefaultSettings()
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktMeta).Get([]byte("settings"))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}

func (s *Store) SaveSettings(v Settings) error {
	raw, _ := json.Marshal(v)
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bktMeta).Put([]byte("settings"), raw)
	})
}

// ---------------------------------------------------------------- stats

func (s *Store) Stats() (Stats, error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if time.Since(s.statsAt) < 3*time.Second {
		return s.statsVal, nil
	}
	var st Stats
	buckets, err := s.ListBuckets()
	if err != nil {
		return st, err
	}
	st.Buckets = len(buckets)
	_ = s.db.View(func(tx *bolt.Tx) error {
		st.Uploads = tx.Bucket(bktUploads).Stats().KeyN
		return nil
	})
	for _, b := range buckets {
		objects, size := s.BucketUsage(b.Name)
		st.Objects += objects
		st.Size += size
	}
	free, total := diskUsage(s.dataDir)
	st.StorageFree, st.StorageTotal = free, total
	s.statsVal = st
	s.statsAt = time.Now()
	return st, nil
}

func (s *Store) invalidateStats() {
	s.statsMu.Lock()
	s.statsAt = time.Time{}
	s.statsMu.Unlock()

	s.usageMu.Lock()
	s.usage = nil
	s.usageMu.Unlock()
}
