package admin

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fnososs/internal/config"
	"fnososs/internal/s3"
	"fnososs/internal/store"
	"fnososs/internal/trimapi"
	"fnososs/internal/version"
	"fnososs/internal/webui"
)

type Activity struct {
	mu      sync.Mutex
	entries []s3.AccessLog
	max     int
}

func NewActivity(max int) *Activity { return &Activity{max: max} }

func (a *Activity) Add(e s3.AccessLog) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	if len(a.entries) > a.max {
		a.entries = a.entries[len(a.entries)-a.max:]
	}
}

func (a *Activity) List(limit int) []s3.AccessLog {
	a.mu.Lock()
	defer a.mu.Unlock()
	if limit <= 0 || limit > len(a.entries) {
		limit = len(a.entries)
	}
	out := make([]s3.AccessLog, 0, limit)
	for i := len(a.entries) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, a.entries[i])
	}
	return out
}

func (a *Activity) CountLast(d time.Duration) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := time.Now().Add(-d)
	n := 0
	for i := len(a.entries) - 1; i >= 0; i-- {
		if a.entries[i].Time.Before(cutoff) {
			break
		}
		n++
	}
	return n
}

type Admin struct {
	Store    *store.Store
	Cfg      config.Config
	Started  time.Time
	Activity *Activity
	assets   fs.FS
}

func New(st *store.Store, cfg config.Config, started time.Time, act *Activity) *Admin {
	sub, err := fs.Sub(webui.Assets, "assets")
	if err != nil {
		panic(err)
	}
	return &Admin{Store: st, Cfg: cfg, Started: started, Activity: act, assets: sub}
}

func (a *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := a.adminPath(r.URL.Path)
	if strings.HasPrefix(p, "/api/") {
		a.serveAPI(w, r, p)
		return
	}
	if p == "/" || p == "" {
		a.serveFile(w, r, "index.html")
		return
	}
	a.serveFile(w, r, strings.TrimPrefix(p, "/"))
}

func (a *Admin) adminPath(p string) string {
	prefix := a.Cfg.GatewayPrefix
	if prefix != "" && prefix != "/" {
		if p == prefix {
			return "/"
		}
		if strings.HasPrefix(p, prefix+"/") {
			p = strings.TrimPrefix(p, prefix)
		}
	}
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func (a *Admin) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	name = path.Clean("/" + name)
	if name == "/" {
		name = "/index.html"
	}
	f, err := a.assets.Open(assetName(name))
	if err != nil {
		// SPA fallback for hash-less deep links.
		f, err = a.assets.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	switch filepath.Ext(name) {
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	default:
		if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	}
	if strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".md") {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	_, _ = w.Write(data)
}

// gatewayUser reads the identity headers that fnOS injects at the unified
// gateway. Direct socket access without these headers is rejected.
func (a *Admin) gatewayUser(r *http.Request) (username string, uid string, admin bool, ok bool) {
	username = r.Header.Get("X-Trim-Username")
	uid = r.Header.Get("X-Trim-Userid")
	admin = strings.EqualFold(r.Header.Get("X-Trim-Isadmin"), "true")
	if username != "" || uid != "" {
		return username, uid, admin, true
	}
	if a.Cfg.DevMode {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			return "dev", "0", true, true
		}
	}
	return "", "", false, false
}

func (a *Admin) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	_, _, admin, ok := a.gatewayUser(r)
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "请从飞牛 fnOS 桌面打开本应用。管理端仅允许通过飞牛统一网关访问。",
			"code":  "gateway_required",
		})
		return false
	}
	if !admin {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "需要飞牛管理员账号才能进入轻量对象存储管理端。",
			"code":  "admin_required",
		})
		return false
	}
	return true
}

func (a *Admin) serveAPI(w http.ResponseWriter, r *http.Request, p string) {
	if p == "/api/session" {
		username, uid, admin, ok := a.gatewayUser(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"user":    map[string]any{"name": username, "uid": uid},
			"isAdmin": admin,
			"gateway": ok,
			"version": version.Version,
			"devMode": a.Cfg.DevMode,
		})
		return
	}
	if !a.requireAdmin(w, r) {
		return
	}

	switch {
	case p == "/api/overview" && r.Method == http.MethodGet:
		a.handleOverview(w, r)
	case p == "/api/activity" && r.Method == http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		writeJSON(w, http.StatusOK, map[string]any{"items": a.Activity.List(limit)})
	case p == "/api/buckets":
		switch r.Method {
		case http.MethodGet:
			a.handleListBuckets(w, r)
		case http.MethodPost:
			a.handleCreateBucket(w, r)
		default:
			methodNotAllowed(w)
		}
	case p == "/api/keys":
		switch r.Method {
		case http.MethodGet:
			a.handleListKeys(w, r)
		case http.MethodPost:
			a.handleCreateKey(w, r)
		default:
			methodNotAllowed(w)
		}
	case p == "/api/backup" && r.Method == http.MethodGet:
		a.handleBackup(w, r)
	case p == "/api/shared-folders" && r.Method == http.MethodGet:
		a.handleSharedFolders(w, r)
	case p == "/api/settings":
		switch r.Method {
		case http.MethodGet:
			a.handleGetSettings(w, r)
		case http.MethodPatch, http.MethodPut:
			a.handlePatchSettings(w, r)
		default:
			methodNotAllowed(w)
		}
	case p == "/api/presign" && r.Method == http.MethodPost:
		a.handlePresign(w, r)
	default:
		parts := splitAPI(p)
		// /api/keys/{access}
		if len(parts) == 2 && parts[0] == "keys" {
			switch r.Method {
			case http.MethodDelete:
				a.handleDeleteKey(w, r, parts[1])
			case http.MethodPatch:
				a.handlePatchKey(w, r, parts[1])
			default:
				methodNotAllowed(w)
			}
			return
		}
		// /api/buckets/{name}/...
		if len(parts) >= 3 && parts[0] == "buckets" {
			name := parts[1]
			switch {
			case len(parts) == 3 && parts[2] == "objects" && r.Method == http.MethodGet:
				a.handleListObjects(w, r, name)
			case len(parts) == 3 && parts[2] == "object":
				switch r.Method {
				case http.MethodGet:
					a.handleGetObject(w, r, name)
				case http.MethodDelete:
					a.handleDeleteObject(w, r, name)
				default:
					methodNotAllowed(w)
				}
			case len(parts) == 3 && parts[2] == "archive" && r.Method == http.MethodGet:
				a.handleArchiveBucket(w, r, name)
			case len(parts) == 3 && parts[2] == "upload" && r.Method == http.MethodPost:
				a.handleUpload(w, r, name)
			case len(parts) == 3 && parts[2] == "policy" && r.Method == http.MethodPatch:
				a.handlePatchPolicy(w, r, name)
			case len(parts) == 3 && parts[2] == "presign" && r.Method == http.MethodPost:
				a.handleObjectPresign(w, r, name)
			default:
				http.NotFound(w, r)
			}
			return
		}
		if len(parts) == 2 && parts[0] == "buckets" {
			switch r.Method {
			case http.MethodDelete:
				a.handleDeleteBucket(w, r, parts[1])
				return
			case http.MethodPatch:
				a.handlePatchBucket(w, r, parts[1])
				return
			}
		}
		http.NotFound(w, r)
	}
}

func splitAPI(p string) []string {
	p = strings.TrimPrefix(p, "/api/")
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func (a *Admin) handleOverview(w http.ResponseWriter, r *http.Request) {
	stats, err := a.Store.Stats()
	if err != nil {
		writeErr(w, err)
		return
	}
	settings, _ := a.Store.Settings()
	buckets, _ := a.Store.ListBuckets()
	keys, _ := a.Store.ListKeys()
	enabled := 0
	for _, k := range keys {
		if k.Enabled {
			enabled++
		}
	}
	username, uid, _, _ := a.gatewayUser(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        version.Version,
		"startedAt":      a.Started,
		"uptimeSeconds":  int64(time.Since(a.Started).Seconds()),
		"region":         a.Cfg.Region,
		"port":           a.Cfg.Port,
		"dataDir":        a.Cfg.DataDir,
		"gatewayPrefix":  a.Cfg.GatewayPrefix,
		"endpoint":       a.endpoint(r),
		"publicEndpoint": settings.PublicEndpoint,
		"lanAddresses":   lanIPv4s(),
		"stats": map[string]any{
			"buckets":      stats.Buckets,
			"objects":      stats.Objects,
			"size":         stats.Size,
			"uploads":      stats.Uploads,
			"storageFree":  stats.StorageFree,
			"storageTotal": stats.StorageTotal,
		},
		"keys":             map[string]any{"total": len(keys), "enabled": enabled},
		"bucketsPreview":   previewBuckets(buckets),
		"requestsLastHour": a.Activity.CountLast(time.Hour),
		"user":             map[string]any{"name": username, "uid": uid},
	})
}

func previewBuckets(bs []store.Bucket) []map[string]any {
	out := make([]map[string]any, 0, len(bs))
	for i, b := range bs {
		if i >= 5 {
			break
		}
		out = append(out, map[string]any{"name": b.Name, "created": b.Created, "policy": b.Policy})
	}
	return out
}

func (a *Admin) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := a.Store.ListBuckets()
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(buckets))
	for _, b := range buckets {
		count, size := a.Store.BucketUsage(b.Name)
		items = append(items, map[string]any{
			"name": b.Name, "created": b.Created, "policy": b.Policy,
			"size": size, "objects": count, "rootPath": b.RootPath,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *Admin) handleCreateBucket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		RootPath string `json:"rootPath"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	b, err := a.Store.CreateBucketAt(strings.TrimSpace(body.Name), body.RootPath)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInvalidBucketName):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "桶名称需为 3-63 位小写字母、数字、点或连字符，且以字母或数字开头结尾。"})
		case errors.Is(err, store.ErrRootPathInUse):
			writeJSON(w, http.StatusConflict, map[string]any{"error": "该目录已绑定到其他存储桶，请选择另一个目录。"})
		case errors.Is(err, store.ErrInvalidRootPath):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "目录不可用：" + err.Error() + "。请确认目录存在且已授权给应用。"})
		case strings.Contains(err.Error(), "exists"):
			writeJSON(w, http.StatusConflict, map[string]any{"error": "同名存储桶已存在。"})
		default:
			writeErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bucket": b})
}

// handleSharedFolders reports which folders the host has authorised for the app.
// It is diagnostic only: a missing token or socket must not break the console,
// so failures are returned as data with available=false.
func (a *Admin) handleSharedFolders(w http.ResponseWriter, r *http.Request) {
	paths, err := trimapi.SharedFolders(r.Context(), "fnos-oss")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"paths":     []string{},
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"paths":     paths,
	})
}

// handleBackup streams a downloadable backup. scope=meta is the consistent
// bbolt snapshot alone (a few hundred KB, enough to recover buckets and keys);
// scope=full adds every stored object and pending upload.
//
// Buckets bound to a user folder are not included: their files live outside the
// data volume and already belong to the user, so they are backed up as folders.
func (a *Admin) handleBackup(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "full"
	}
	stamp := time.Now().Format("20060102-150405")

	switch scope {
	case "meta":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "fnos-oss-meta-" + stamp + ".db"}))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = a.Store.WriteSnapshot(w)
		return
	case "full":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scope 只能是 meta 或 full。"})
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "fnos-oss-backup-" + stamp + ".tar.gz"}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)

	// The snapshot has no known length up front, so buffer it once. meta.db is
	// small; object blobs are streamed straight from disk.
	snap, err := os.CreateTemp("", "fnos-oss-meta-*.db")
	if err != nil {
		return
	}
	defer os.Remove(snap.Name())
	if err := a.Store.WriteSnapshot(snap); err != nil {
		snap.Close()
		return
	}
	info, err := snap.Stat()
	if err != nil {
		snap.Close()
		return
	}
	if _, err := snap.Seek(0, io.SeekStart); err != nil {
		snap.Close()
		return
	}
	err = addTarFile(tw, "meta.db", info.Size(), info.ModTime(), snap)
	snap.Close()
	if err != nil {
		return
	}

	root := a.Store.DataDir()
	for _, dir := range []string{"objects", "uploads"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return nil
			}
			name := filepath.ToSlash(rel)
			if d.IsDir() {
				_ = tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: time.Now()})
				return nil
			}
			// Objects are installed by rename, so a file that disappears here
			// was deleted mid-backup; skipping it keeps the archive readable.
			f, oerr := os.Open(p)
			if oerr != nil {
				return nil
			}
			st, serr := f.Stat()
			if serr != nil {
				f.Close()
				return nil
			}
			_ = addTarFile(tw, name, st.Size(), st.ModTime(), f)
			f.Close()
			return nil
		})
	}

	if err := tw.Close(); err != nil {
		return
	}
	_ = gz.Close()
}

func addTarFile(tw *tar.Writer, name string, size int64, mod time.Time, body io.Reader) error {
	h := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: size, ModTime: mod}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := io.Copy(tw, body)
	return err
}

// handlePatchBucket re-points a bucket at another folder. Files never move:
// after restoring a data volume on a new NAS the folder usually has a different
// path, and this is how the bucket is reconnected to it.
func (a *Admin) handlePatchBucket(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		RootPath *string `json:"rootPath"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.RootPath == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 rootPath；传入空字符串表示改回应用数据卷。"})
		return
	}
	b, err := a.Store.SetBucketRoot(name, *body.RootPath)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrBucketNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "存储桶不存在。"})
		return
	case errors.Is(err, store.ErrRootPathInUse):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "该目录已绑定到其他存储桶，请选择另一个目录。"})
		return
	case errors.Is(err, store.ErrInvalidRootPath):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "目录不可用：" + err.Error() + "。请确认目录存在且已授权给应用。"})
		return
	default:
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bucket": b})
}

func (a *Admin) handleDeleteBucket(w http.ResponseWriter, r *http.Request, name string) {
	err := a.Store.DeleteBucket(name)
	switch err {
	case nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case store.ErrBucketNotFound:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "存储桶不存在。"})
	case store.ErrBucketNotEmpty:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "存储桶非空，请先删除其中的对象。"})
	default:
		writeErr(w, err)
	}
}

func (a *Admin) handlePatchPolicy(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Policy string `json:"policy"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Policy != "private" && body.Policy != "public-read" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "策略只能是 private 或 public-read。"})
		return
	}
	if err := a.Store.SetBucketPolicy(name, body.Policy); err != nil {
		if err == store.ErrBucketNotFound {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "存储桶不存在。"})
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "policy": body.Policy})
}

func (a *Admin) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	after := q.Get("after")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	res, err := a.Store.ListObjects(bucket, prefix, delimiter, after, limit)
	if err != nil {
		if err == store.ErrBucketNotFound {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "存储桶不存在。"})
			return
		}
		writeErr(w, err)
		return
	}
	next := ""
	if res.IsTruncated && len(res.Objects) > 0 {
		next = res.Objects[len(res.Objects)-1].Key
	}
	items := make([]map[string]any, 0, len(res.Objects))
	for _, o := range res.Objects {
		items = append(items, map[string]any{
			"key": o.Key, "size": o.Size, "etag": o.ETag,
			"modified": o.ModTime, "contentType": o.ContentType,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "prefixes": res.CommonPrefixes,
		"prefix": prefix, "truncated": res.IsTruncated, "next": next,
	})
}

func (a *Admin) handleGetObject(w http.ResponseWriter, r *http.Request, bucket string) {
	key := r.URL.Query().Get("key")
	f, o, err := a.Store.OpenObject(bucket, key)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "对象不存在。"})
		return
	}
	defer f.Close()
	if r.URL.Query().Get("meta") == "1" {
		writeJSON(w, http.StatusOK, map[string]any{"object": o})
		return
	}
	w.Header().Set("Content-Type", o.ContentType)
	w.Header().Set("ETag", `"`+o.ETag+`"`)
	if r.URL.Query().Get("download") == "1" {
		// FormatMediaType escapes non-ASCII names as filename*=utf-8''…, which
		// keeps Chinese object names from turning into mojibake.
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(o.Key)}))
	}
	http.ServeContent(w, r, o.Key, o.ModTime, f)
}

// handleArchiveBucket streams a bucket (or one prefix inside it) as a zip.
// The console only loads 500 rows at a time, so this is the only way to hand a
// user a complete copy without asking them to install an S3 client.
func (a *Admin) handleArchiveBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.Store.GetBucket(bucket); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "存储桶不存在。"})
		return
	}
	prefix := r.URL.Query().Get("prefix")
	fileName := bucket + ".zip"
	if trimmed := strings.Trim(prefix, "/"); trimmed != "" {
		fileName = path.Base(trimmed) + ".zip"
	}
	// A zip is streamed, so its length is unknown up front and the browser
	// cannot show a progress bar. Keep it out of caches either way.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	zw := zip.NewWriter(w)
	defer zw.Close()

	after := ""
	for {
		res, err := a.Store.ListObjects(bucket, prefix, "", after, 500)
		if err != nil {
			// Headers are already sent, so the only honest signal left is to
			// stop writing: the archive ends truncated instead of looking big.
			return
		}
		for _, o := range res.Objects {
			if err := writeZipEntry(zw, a.Store, bucket, o); err != nil {
				// An object can vanish between the listing and the read. Skip
				// it rather than failing the whole download.
				continue
			}
		}
		if !res.IsTruncated || len(res.Objects) == 0 {
			return
		}
		after = res.Objects[len(res.Objects)-1].Key
	}
}

func writeZipEntry(zw *zip.Writer, st *store.Store, bucket string, o store.Object) error {
	f, meta, err := st.OpenObject(bucket, o.Key)
	if err != nil {
		return err
	}
	defer f.Close()
	h := &zip.FileHeader{Name: o.Key, Method: zip.Deflate, Modified: meta.ModTime}
	h.SetMode(0o644)
	dst, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, f)
	return err
}

func (a *Admin) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket string) {
	key := r.URL.Query().Get("key")
	if _, err := a.Store.DeleteObject(bucket, key); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *Admin) handleUpload(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.Store.GetBucket(bucket); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "存储桶不存在。"})
		return
	}
	// Allow generous admin uploads but never buffer the whole object in memory.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<30)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请使用 multipart/form-data 上传。"})
		return
	}
	prefix := r.URL.Query().Get("prefix")
	key := ""
	var filePart io.Reader
	contentType := "application/octet-stream"
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取上传内容失败。"})
			return
		}
		name := part.FormName()
		if name == "key" {
			raw, _ := io.ReadAll(io.LimitReader(part, 4096))
			key = string(raw)
			continue
		}
		if name == "file" {
			filePart = part
			if part.FileName() != "" {
				key = part.FileName()
			}
			if ct := part.Header.Get("Content-Type"); ct != "" {
				contentType = ct
			}
			break
		}
	}
	if filePart == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 file 字段。"})
		return
	}
	key = strings.TrimPrefix(path.Clean("/"+key), "/")
	if key == "" || key == "." || strings.HasSuffix(key, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "对象名称无效。"})
		return
	}
	key = prefix + key
	if !store.ValidObjectKey(key) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "对象名称无效。"})
		return
	}
	tmp, err := a.Store.TempFile()
	if err != nil {
		writeErr(w, err)
		return
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = removeFile(tmpName)
	}()
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), filePart)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "上传中断：" + err.Error()})
		return
	}
	if err := tmp.Close(); err != nil {
		writeErr(w, err)
		return
	}
	meta := store.Object{
		Key: key, Size: n, ETag: fmt.Sprintf("%x", h.Sum(nil)),
		ModTime: time.Now().UTC(), ContentType: contentType, StorageClass: "STANDARD",
	}
	if err := a.Store.SaveObjectFile(bucket, meta, tmpName); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": meta})
}

func (a *Admin) handleListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.Store.ListKeys()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": keys})
}

func (a *Admin) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label    string   `json:"label"`
		Buckets  []string `json:"buckets"`
		ReadOnly bool     `json:"readOnly"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	buckets, err := a.validateKeyBuckets(body.Buckets)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	k, err := a.Store.CreateKeyScoped(strings.TrimSpace(body.Label), buckets, body.ReadOnly)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": k})
}

// validateKeyBuckets drops duplicates and rejects names that no longer exist, so
// a key can never be scoped to a bucket that is not there.
func (a *Admin) validateKeyBuckets(in []string) ([]string, error) {
	out := make([]string, 0, 1)
	seen := make(map[string]bool, len(in))
	for _, b := range in {
		b = strings.TrimSpace(b)
		if b == "" || seen[b] {
			continue
		}
		if _, err := a.Store.GetBucket(b); err != nil {
			return nil, fmt.Errorf("存储桶不存在：%s", b)
		}
		seen[b] = true
		out = append(out, b)
	}
	if len(out) > 1 {
		return nil, fmt.Errorf("一个访问密钥只能绑定一个存储桶；需要访问全部桶时请不要选择。")
	}
	return out, nil
}

func (a *Admin) handlePatchKey(w http.ResponseWriter, r *http.Request, access string) {
	var body struct {
		Enabled  *bool     `json:"enabled"`
		Buckets  *[]string `json:"buckets"`
		ReadOnly *bool     `json:"readOnly"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Enabled != nil {
		if err := a.Store.SetKeyEnabled(access, *body.Enabled); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "访问密钥不存在。"})
			return
		}
	}
	if body.Buckets != nil || body.ReadOnly != nil {
		k, err := a.Store.GetKey(access)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "访问密钥不存在。"})
			return
		}
		buckets := k.Buckets
		readOnly := k.ReadOnly
		if body.Buckets != nil {
			buckets, err = a.validateKeyBuckets(*body.Buckets)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
		if body.ReadOnly != nil {
			readOnly = *body.ReadOnly
		}
		if err := a.Store.SetKeyScope(access, buckets, readOnly); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "访问密钥不存在。"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *Admin) handleDeleteKey(w http.ResponseWriter, r *http.Request, access string) {
	if err := a.Store.DeleteKey(access); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *Admin) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Store.Settings()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": settings,
		"network": map[string]any{
			"port": a.Cfg.Port, "region": a.Cfg.Region,
			"gatewayPrefix": a.Cfg.GatewayPrefix, "endpoint": a.endpoint(r),
			"lanAddresses": lanIPv4s(), "dataDir": a.Cfg.DataDir,
		},
	})
}

func (a *Admin) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CORSOrigins    *[]string `json:"corsOrigins"`
		MaxUploadSize  *int64    `json:"maxUploadSize"`
		PublicEndpoint *string   `json:"publicEndpoint"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	settings, err := a.Store.Settings()
	if err != nil {
		writeErr(w, err)
		return
	}
	if body.CORSOrigins != nil {
		var cleaned []string
		for _, o := range *body.CORSOrigins {
			o = strings.TrimSpace(o)
			if o != "" {
				cleaned = append(cleaned, o)
			}
		}
		if len(cleaned) == 0 {
			cleaned = []string{}
		}
		settings.CORSOrigins = cleaned
	}
	if body.MaxUploadSize != nil {
		settings.MaxUploadSize = *body.MaxUploadSize
	}
	if body.PublicEndpoint != nil {
		settings.PublicEndpoint = strings.TrimRight(strings.TrimSpace(*body.PublicEndpoint), "/")
	}
	if err := a.Store.SaveSettings(settings); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (a *Admin) handlePresign(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Bucket   string `json:"bucket"`
		Key      string `json:"key"`
		Method   string `json:"method"`
		Expires  int    `json:"expires"`
		Endpoint string `json:"endpoint"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.presign(w, r, body.Bucket, body.Key, body.Method, body.Expires, body.Endpoint)
}

func (a *Admin) handleObjectPresign(w http.ResponseWriter, r *http.Request, bucket string) {
	var body struct {
		Key      string `json:"key"`
		Method   string `json:"method"`
		Expires  int    `json:"expires"`
		Endpoint string `json:"endpoint"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.presign(w, r, bucket, body.Key, body.Method, body.Expires, body.Endpoint)
}

func (a *Admin) presign(w http.ResponseWriter, r *http.Request, bucket, key, method string, expires int, endpoint string) {
	if _, err := a.Store.GetBucket(bucket); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "存储桶不存在。"})
		return
	}
	if !store.ValidObjectKey(key) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "对象名称无效。"})
		return
	}
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPut {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "只支持 GET 或 PUT 分享链接。"})
		return
	}
	if expires <= 0 || expires > 604800 {
		expires = 3600
	}
	keys, _ := a.Store.ListKeys()
	var chosen *store.APIKey
	for i := range keys {
		k := &keys[i]
		if !k.Enabled || !k.AllowsBucket(bucket) {
			continue
		}
		if k.ReadOnly && method != http.MethodGet {
			continue
		}
		chosen = k
		break
	}
	if chosen == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "没有同时满足该存储桶与请求方法的启用密钥，请调整密钥范围。"})
		return
	}
	base := endpoint
	if base == "" {
		settings, _ := a.Store.Settings()
		base = settings.PublicEndpoint
	}
	if base == "" {
		base = a.endpoint(r)
	}
	link, err := s3.Presign(method, base, bucket, key, a.Cfg.Region, chosen.AccessKey, chosen.SecretKey, time.Duration(expires)*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "生成分享链接失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": link, "method": method, "expires": expires})
}

// endpoint returns the address clients should use for the S3 API. A configured
// public endpoint wins; otherwise the console shows the NAS LAN address on the
// S3 port, because the browser host is the gateway domain which does not serve
// the S3 listener. The S3 API is plain HTTP unless a reverse proxy fronts it.
func (a *Admin) endpoint(r *http.Request) string {
	settings, _ := a.Store.Settings()
	if settings.PublicEndpoint != "" {
		return settings.PublicEndpoint
	}
	if ip := lanIPv4(); ip != "" {
		return fmt.Sprintf("http://%s:%d", ip, a.Cfg.Port)
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, a.Cfg.Port)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "请求方法不支持。"})
}

var methodNotAllowed = writeMethodNotAllowed

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求内容不是有效的 JSON。"})
		return false
	}
	return true
}

func removeFile(name string) error {
	return os.Remove(name)
}

// assetName maps a request path such as /assets/app.js to a name inside the
// embedded assets directory.
func assetName(cleanPath string) string {
	name := strings.TrimPrefix(cleanPath, "/")
	name = strings.TrimPrefix(name, "assets/")
	return name
}
