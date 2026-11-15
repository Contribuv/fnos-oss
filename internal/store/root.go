package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	bolt "go.etcd.io/bbolt"
)

// A bucket bound to an fnOS folder stores object keys as real files, so the
// content is readable from the file manager, SMB and other apps. The metadata
// index is still kept for Content-Type, ETag and custom headers, but the
// filesystem is the source of truth: anything dropped into the folder shows up
// through S3 as well.
var (
	ErrInvalidRootPath = errors.New("invalid root path")
	ErrRootPathInUse   = errors.New("root path already bound to another bucket")
	ErrKeyConflict     = errors.New("object key conflicts with an existing file or directory")
)

// errStopWalk ends a directory walk once the requested page is full.
var errStopWalk = errors.New("stop walk")

// normalizeRoot cleans a user supplied folder. An empty value keeps the bucket
// on the internal layout.
func normalizeRoot(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if strings.ContainsRune(p, 0) {
		return "", ErrInvalidRootPath
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", ErrInvalidRootPath
	}
	abs = filepath.Clean(abs)
	if abs == string(filepath.Separator) {
		return "", fmt.Errorf("%w: 不能直接使用磁盘根目录", ErrInvalidRootPath)
	}
	return abs, nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && filepath.Clean(ra) == filepath.Clean(rb)
}

// fsObjectPath maps an object key to a file below root. Segment-wise validation
// keeps "..", empty segments and absolute paths out of the filesystem.
func fsObjectPath(root, key string) (string, error) {
	if root == "" {
		return "", ErrInvalidKey
	}
	if !ValidObjectKey(key) || strings.HasSuffix(key, "/") {
		return "", ErrInvalidKey
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", ErrInvalidKey
		}
	}
	full := filepath.Join(root, filepath.FromSlash(key))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", ErrInvalidKey
	}
	return full, nil
}

func pseudoETag(fi fs.FileInfo) string {
	return fmt.Sprintf("%x-%x", fi.Size(), fi.ModTime().UnixNano())
}

func contentTypeForKey(key string) string {
	if ct := mime.TypeByExtension(path.Ext(key)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func sameModTime(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d < time.Millisecond
}

// ---------------------------------------------------------------- write

func (s *Store) saveObjectToRoot(b Bucket, meta Object, src string) error {
	dst, err := fsObjectPath(b.RootPath, meta.Key)
	if err != nil {
		return err
	}
	if fi, statErr := os.Stat(dst); statErr == nil && fi.IsDir() {
		return ErrKeyConflict
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return mapPathError(err)
	}
	if err := os.Rename(src, dst); err != nil {
		if err := copyFile(src, dst); err != nil {
			return mapPathError(err)
		}
		_ = os.Remove(src)
	}
	now := time.Now().UTC()
	_ = os.Chtimes(dst, now, now)
	fi, err := os.Stat(dst)
	if err != nil {
		return err
	}
	meta.Size = fi.Size()
	meta.ModTime = fi.ModTime().UTC()
	if meta.ContentType == "" {
		meta.ContentType = contentTypeForKey(meta.Key)
	}
	if meta.StorageClass == "" {
		meta.StorageClass = "STANDARD"
	}
	raw, _ := json.Marshal(meta)
	err = s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bktObjects).Put(objKey(b.Name, meta.Key), raw)
	})
	if err != nil {
		return err
	}
	s.invalidateStats()
	return nil
}

func mapPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrExist) || isNotDir(err) {
		return ErrKeyConflict
	}
	return err
}

// isNotDir reports whether a path lookup failed because a parent component is a
// regular file, which happens when an S3 key such as "a" blocks "a/b".
func isNotDir(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not a directory")
}

// ---------------------------------------------------------------- read

func (s *Store) getObjectFromRoot(b Bucket, key string) (Object, error) {
	full, err := fsObjectPath(b.RootPath, key)
	if err != nil {
		return Object{}, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) || isNotDir(err) {
			return Object{}, ErrObjectNotFound
		}
		return Object{}, err
	}
	if fi.IsDir() {
		return Object{}, ErrObjectNotFound
	}
	o := Object{
		Key:          key,
		Size:         fi.Size(),
		ModTime:      fi.ModTime().UTC(),
		StorageClass: "STANDARD",
		ContentType:  contentTypeForKey(key),
		ETag:         pseudoETag(fi),
	}
	// The index wins only while it still matches the file on disk; anything the
	// user changed in the file manager must not keep stale headers or ETags.
	if rec, ok := s.indexedObject(b.Name, key); ok && rec.Size == o.Size && sameModTime(rec.ModTime, o.ModTime) {
		rec.Key = key
		return rec, nil
	}
	return o, nil
}

func (s *Store) indexedObject(bucket, key string) (Object, bool) {
	var out Object
	found := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktObjects).Get(objKey(bucket, key))
		if raw == nil {
			return nil
		}
		o, err := decode[Object](raw)
		if err != nil {
			return nil
		}
		out, found = o, true
		return nil
	})
	return out, found
}

// ---------------------------------------------------------------- delete

func (s *Store) deleteFromRoot(b Bucket, key string) (bool, error) {
	full, err := fsObjectPath(b.RootPath, key)
	if err != nil {
		return false, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) || isNotDir(err) {
			return false, nil
		}
		return false, err
	}
	if fi.IsDir() {
		return false, nil
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	_ = s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bktObjects).Delete(objKey(b.Name, key))
	})
	pruneEmptyDirs(b.RootPath, full)
	s.invalidateStats()
	return true, nil
}

// pruneEmptyDirs removes the folders an object left behind, but never touches
// the bucket root or anything outside it.
func pruneEmptyDirs(root, file string) {
	dir := filepath.Dir(file)
	root = filepath.Clean(root)
	for strings.HasPrefix(dir, root+string(os.PathSeparator)) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ---------------------------------------------------------------- list

func (s *Store) listObjectsFromRoot(b Bucket, prefix, delimiter, after string, max int) (ListResult, error) {
	res := ListResult{}
	seen := map[string]bool{}
	count := 0
	lastMarker := ""

	// Start at the folder that contains the prefix instead of walking the whole
	// tree; a prefix without a slash starts at the root.
	startRel := ""
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		startRel = prefix[:i]
	}
	start := b.RootPath
	if startRel != "" {
		start = filepath.Join(b.RootPath, filepath.FromSlash(startRel))
	}
	if fi, err := os.Stat(start); err != nil || !fi.IsDir() {
		return res, nil
	}

	truncated := false
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(b.RootPath, p)
		if err != nil || rel == "." {
			return nil
		}
		key := filepath.ToSlash(rel)

		if d.IsDir() {
			// Never follow symlinked folders: they can escape the authorised root.
			if d.Type()&os.ModeSymlink != 0 {
				return fs.SkipDir
			}
			dirKey := key + "/"
			if prefix != "" && !strings.HasPrefix(prefix, dirKey) && !strings.HasPrefix(dirKey, prefix) {
				return fs.SkipDir
			}
			if delimiter != "" && strings.HasPrefix(dirKey, prefix) {
				rest := dirKey[len(prefix):]
				if idx := strings.Index(rest, delimiter); idx >= 0 {
					cp := prefix + rest[:idx+len(delimiter)]
					if after != "" && cp <= after {
						return fs.SkipDir
					}
					if !seen[cp] {
						if count >= max {
							truncated = true
							return errStopWalk
						}
						seen[cp] = true
						res.CommonPrefixes = append(res.CommonPrefixes, cp)
						count++
						lastMarker = cp
					}
					return fs.SkipDir
				}
			}
			return nil
		}

		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		if after != "" && key <= after {
			return nil
		}
		if delimiter != "" {
			rest := key[len(prefix):]
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				if !seen[cp] {
					if count >= max {
						truncated = true
						return errStopWalk
					}
					seen[cp] = true
					res.CommonPrefixes = append(res.CommonPrefixes, cp)
					count++
					lastMarker = cp
				}
				return nil
			}
		}
		if count >= max {
			truncated = true
			return errStopWalk
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		res.Objects = append(res.Objects, Object{
			Key:          key,
			Size:         fi.Size(),
			ModTime:      fi.ModTime().UTC(),
			StorageClass: "STANDARD",
			ContentType:  contentTypeForKey(key),
			ETag:         pseudoETag(fi),
		})
		count++
		lastMarker = key
		return nil
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		return res, err
	}
	res.IsTruncated = truncated
	if truncated {
		res.NextMarker = lastMarker
	}
	res.KeyCount = len(res.Objects) + len(res.CommonPrefixes)
	return res, nil
}

// ---------------------------------------------------------------- usage

func (s *Store) usageFromRoot(root string) (objects int64, size int64) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if err != nil && d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			objects++
			size += fi.Size()
		}
		return nil
	})
	return objects, size
}

func dirIsEmpty(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return len(entries) == 0, nil
}
