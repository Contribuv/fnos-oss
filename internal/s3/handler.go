package s3

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"fnososs/internal/store"
)

type AccessLog struct {
	Time      time.Time     `json:"time"`
	Method    string        `json:"method"`
	Path      string        `json:"path"`
	Status    int           `json:"status"`
	Bytes     int64         `json:"bytes"`
	Duration  time.Duration `json:"duration"`
	AccessKey string        `json:"accessKey,omitempty"`
	Error     string        `json:"error,omitempty"`
}

type Server struct {
	Store     *store.Store
	Region    string
	Logger    *slog.Logger
	OnRequest func(AccessLog)
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *responseRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func requestID(r *http.Request) string {
	if v := r.Header.Get("x-amz-request-id"); v != "" {
		return v
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &responseRecorder{ResponseWriter: w}
	reqID := newRequestID()
	rec.Header().Set("x-amz-request-id", reqID)
	rec.Header().Set("Server", "fnos-oss")

	var ai *authInfo
	defer func() {
		if p := recover(); p != nil {
			s.Logger.Error("panic while handling request", "path", r.URL.Path, "panic", p)
			if rec.status == 0 {
				writeError(rec, r, errInternal)
			}
		}
		if s.OnRequest != nil {
			entry := AccessLog{
				Time: start, Method: r.Method, Path: r.URL.Path,
				Status: rec.status, Bytes: rec.bytes, Duration: time.Since(start),
			}
			if ai != nil {
				entry.AccessKey = ai.AccessKey
			}
			s.OnRequest(entry)
		}
	}()

	if r.URL.Path == "/__health" {
		rec.Header().Set("Content-Type", "application/json")
		_, _ = rec.Write([]byte(`{"ok":true}`))
		return
	}

	s.applyCORS(rec, r)
	if r.Method == http.MethodOptions {
		rec.WriteHeader(http.StatusOK)
		return
	}

	bucket, key := splitPath(r.URL.Path)

	var err error
	ai, err = s.authenticate(r)
	if err != nil {
		s.Logger.Warn("s3 authentication failed", "path", r.URL.Path, "error", err)
		writeError(rec, r, err)
		return
	}

	if err := s.authorize(r, bucket, key, ai); err != nil {
		writeError(rec, r, err)
		return
	}

	if ai != nil {
		s.Store.TouchKey(ai.AccessKey)
	}

	if err := s.route(rec, r, bucket, key, ai); err != nil {
		writeError(rec, r, wrapErr(err))
	}
}

func splitPath(p string) (bucket, key string) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", ""
	}
	bucket, key, _ = strings.Cut(p, "/")
	return bucket, key
}

func (s *Server) authorize(r *http.Request, bucket, key string, ai *authInfo) error {
	if ai != nil {
		return nil
	}
	if bucket == "" {
		return errAccessDenied
	}
	b, err := s.Store.GetBucket(bucket)
	if err != nil {
		return errNoSuchBucket
	}
	if b.Policy == "public-read" && (r.Method == http.MethodGet || r.Method == http.MethodHead) && key != "" {
		return nil
	}
	return errAccessDenied.withMessage("Anonymous access is only allowed for objects in public-read buckets.")
}

func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	settings, err := s.Store.Settings()
	if err != nil {
		return
	}
	allowed := false
	for _, o := range settings.CORSOrigins {
		if o == "*" || o == origin {
			allowed = true
			break
		}
	}
	if !allowed {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, HEAD, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Expose-Headers", "ETag, x-amz-request-id, x-amz-version-id, Content-Range, Accept-Ranges")
	w.Header().Set("Access-Control-Max-Age", "3600")
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, bucket, key string, ai *authInfo) error {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		if bucket == "" {
			return s.handleListBuckets(w, r, ai)
		}
		if key == "" {
			switch {
			case q.Has("location"):
				return s.handleGetLocation(w, r)
			case q.Has("versioning"):
				return s.handleGetVersioning(w, r)
			case q.Has("uploads"):
				return s.handleListUploads(w, r, bucket, q)
			case q.Has("acl"):
				return s.handleGetACL(w, r)
			case q.Has("policy"):
				return newError("NoSuchBucketPolicy", "The bucket policy does not exist.", http.StatusNotFound)
			case q.Has("cors"):
				return s.handleGetCORS(w, r)
			default:
				return s.handleListObjects(w, r, bucket, q)
			}
		}
		if q.Get("uploadId") != "" {
			return s.handleListParts(w, r, bucket, key, q)
		}
		if q.Has("acl") {
			return s.handleGetACL(w, r)
		}
		return s.handleGetObject(w, r, bucket, key, false)
	case http.MethodHead:
		if key == "" {
			if _, err := s.Store.GetBucket(bucket); err != nil {
				return errNoSuchBucket
			}
			w.WriteHeader(http.StatusOK)
			return nil
		}
		return s.handleGetObject(w, r, bucket, key, true)
	case http.MethodPut:
		if key == "" {
			if q.Has("versioning") {
				return s.handlePutVersioning(w, r, bucket)
			}
			if q.Has("acl") {
				w.WriteHeader(http.StatusOK)
				return nil
			}
			return s.handleCreateBucket(w, r, bucket)
		}
		if q.Get("partNumber") != "" && q.Get("uploadId") != "" {
			return s.handleUploadPart(w, r, bucket, key, q, ai)
		}
		if r.Header.Get("x-amz-copy-source") != "" {
			return s.handleCopyObject(w, r, bucket, key, ai)
		}
		return s.handlePutObject(w, r, bucket, key, ai)
	case http.MethodPost:
		if key == "" && q.Has("delete") {
			return s.handleDeleteObjects(w, r, bucket)
		}
		if key != "" && q.Has("uploads") {
			return s.handleCreateUpload(w, r, bucket, key)
		}
		if key != "" && q.Get("uploadId") != "" {
			return s.handleCompleteUpload(w, r, bucket, key, q)
		}
		return errMethodNotAllowed
	case http.MethodDelete:
		if key == "" {
			return s.handleDeleteBucket(w, r, bucket)
		}
		if q.Get("uploadId") != "" {
			return s.handleAbortUpload(w, r, bucket, key, q)
		}
		return s.handleDeleteObject(w, r, bucket, key)
	default:
		return errMethodNotAllowed
	}
}

// ---------------------------------------------------------------- bucket ops

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request, ai *authInfo) error {
	if ai == nil {
		return errAccessDenied
	}
	buckets, err := s.Store.ListBuckets()
	if err != nil {
		return err
	}
	out := listAllBucketsResult{NS: s3NS, Owner: owner()}
	for _, b := range buckets {
		out.Buckets = append(out.Buckets, xmlBucket{Name: b.Name, CreationDate: isoTime(b.Created)})
	}
	return writeXML(w, http.StatusOK, out)
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) error {
	if !store.ValidBucketName(bucket) {
		return errInvalidBucketName
	}
	if _, err := s.Store.CreateBucket(bucket); err != nil {
		if strings.Contains(err.Error(), "exists") {
			return errBucketExists
		}
		return err
	}
	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
	return nil
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) error {
	err := s.Store.DeleteBucket(bucket)
	switch err {
	case nil:
		w.WriteHeader(http.StatusNoContent)
		return nil
	case store.ErrBucketNotFound:
		return errNoSuchBucket
	case store.ErrBucketNotEmpty:
		return errBucketNotEmpty
	default:
		return err
	}
}

func (s *Server) handleGetLocation(w http.ResponseWriter, r *http.Request) error {
	value := s.Region
	if value == "us-east-1" {
		value = ""
	}
	return writeXML(w, http.StatusOK, locationConstraint{NS: s3NS, Value: value})
}

func (s *Server) handleGetVersioning(w http.ResponseWriter, r *http.Request) error {
	return writeXML(w, http.StatusOK, versioningConfiguration{NS: s3NS})
}

func (s *Server) handlePutVersioning(w http.ResponseWriter, r *http.Request, bucket string) error {
	// Versioning is not implemented; accept the call so clients can proceed.
	if _, err := s.Store.GetBucket(bucket); err != nil {
		return errNoSuchBucket
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (s *Server) handleGetACL(w http.ResponseWriter, r *http.Request) error {
	const acl = `<?xml version="1.0" encoding="UTF-8"?>
<AccessControlPolicy xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Owner><ID>fnos</ID><DisplayName>fnos</DisplayName></Owner>
  <AccessControlList><Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser"><ID>fnos</ID><DisplayName>fnos</DisplayName></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList>
</AccessControlPolicy>`
	w.Header().Set("Content-Type", "application/xml")
	_, err := w.Write([]byte(acl))
	return err
}

func (s *Server) handleGetCORS(w http.ResponseWriter, r *http.Request) error {
	return newError("NoSuchCORSConfiguration", "The CORS configuration does not exist.", http.StatusNotFound)
}

// ---------------------------------------------------------------- listing

func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string, q url.Values) error {
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return errInvalidArgument.withMessage("Invalid max-keys.")
		}
		if n < maxKeys {
			maxKeys = n
		}
	}
	after := ""
	v2 := q.Get("list-type") == "2"
	if v2 {
		if tok := q.Get("continuation-token"); tok != "" {
			raw, err := base64.RawURLEncoding.DecodeString(tok)
			if err == nil {
				after = string(raw)
			}
		} else {
			after = q.Get("start-after")
		}
	} else {
		after = q.Get("marker")
	}
	if maxKeys <= 0 {
		maxKeys = 1
	}
	res, err := s.Store.ListObjects(bucket, prefix, delimiter, after, maxKeys)
	if err != nil {
		if err == store.ErrBucketNotFound {
			return errNoSuchBucket
		}
		return err
	}
	out := listBucketResult{
		NS: s3NS, Name: bucket, Prefix: prefix, Delimiter: delimiter,
		MaxKeys: maxKeys, IsTruncated: res.IsTruncated, KeyCount: res.KeyCount,
	}
	if v2 {
		out.ContinuationToken = q.Get("continuation-token")
		out.StartAfter = q.Get("start-after")
		if res.IsTruncated {
			out.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(res.NextMarker))
		}
	} else {
		out.Marker = q.Get("marker")
		if res.IsTruncated {
			out.NextMarker = res.NextMarker
		}
	}
	for _, o := range res.Objects {
		out.Contents = append(out.Contents, xmlObject{
			Key: o.Key, LastModified: isoTime(o.ModTime), ETag: quoteETag(o.ETag),
			Size: o.Size, StorageClass: orDefault(o.StorageClass, "STANDARD"),
		})
	}
	for _, p := range res.CommonPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, xmlCommonPrefix{Prefix: p})
	}
	return writeXML(w, http.StatusOK, out)
}

func (s *Server) handleListUploads(w http.ResponseWriter, r *http.Request, bucket string, q url.Values) error {
	if _, err := s.Store.GetBucket(bucket); err != nil {
		return errNoSuchBucket
	}
	uploads, err := s.Store.ListUploads(bucket)
	if err != nil {
		return err
	}
	prefix := q.Get("prefix")
	max := 1000
	if v := q.Get("max-uploads"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < max {
			max = n
		}
	}
	out := listUploadsResult{NS: s3NS, Bucket: bucket, MaxUploads: max}
	for _, u := range uploads {
		if prefix != "" && !strings.HasPrefix(u.Key, prefix) {
			continue
		}
		if len(out.Uploads) >= max {
			out.IsTruncated = true
			out.NextKeyMarker = u.Key
			out.NextUploadIDMarker = u.UploadID
			break
		}
		out.Uploads = append(out.Uploads, xmlUpload{
			Key: u.Key, UploadID: u.UploadID, Initiated: isoTime(u.Created),
			StorageClass: orDefault(u.StorageClass, "STANDARD"),
		})
	}
	return writeXML(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- object ops

func objectFromRequest(r *http.Request, key string) store.Object {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(key))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	headers := map[string]string{}
	for _, h := range []string{"Cache-Control", "Content-Disposition", "Content-Language", "Expires"} {
		if v := r.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	if ce := r.Header.Get("Content-Encoding"); ce != "" && !strings.Contains(ce, "aws-chunked") {
		headers["Content-Encoding"] = ce
	}
	userMeta := map[string]string{}
	for name, vals := range r.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-meta-") && len(vals) > 0 {
			userMeta[lower[len("x-amz-meta-"):]] = vals[0]
		}
	}
	return store.Object{
		Key: key, ContentType: ct, StorageClass: "STANDARD",
		Headers: headers, UserMeta: userMeta,
	}
}

func (s *Server) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string, ai *authInfo) error {
	if _, err := s.Store.GetBucket(bucket); err != nil {
		return errNoSuchBucket
	}
	if !store.ValidObjectKey(key) {
		return errInvalidArgument.withMessage("Invalid object key.")
	}
	maxSize := int64(0)
	if settings, err := s.Store.Settings(); err == nil {
		maxSize = settings.MaxUploadSize
	}
	if maxSize > 0 && r.ContentLength > maxSize {
		return errEntityTooLarge
	}
	body, err := decodeBody(r.Body, ai, r.Header)
	if err != nil {
		return err
	}
	tmp, err := s.Store.TempFile()
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = removeIfExists(tmpName)
	}()

	md5Hash := md5.New()
	shaHash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, md5Hash, shaHash), body)
	if err != nil {
		return errInvalidArgument.withMessage("Failed to read request body: " + err.Error())
	}
	if r.ContentLength >= 0 && !isChunkedEncoding(r) && n != r.ContentLength {
		return errIncompleteBody
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if v := r.Header.Get("X-Amz-Decoded-Content-Length"); v != "" {
		if want, err := strconv.ParseInt(v, 10, 64); err == nil && n != want {
			return errIncompleteBody
		}
	}
	if maxSize > 0 && n > maxSize {
		return errEntityTooLarge
	}
	if v := r.Header.Get("Content-MD5"); v != "" {
		want, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return errInvalidArgument.withMessage("Invalid Content-MD5.")
		}
		if !hmacEqualBytes(want, md5Hash.Sum(nil)) {
			return newError("BadDigest", "The Content-MD5 you specified did not match what we received.", http.StatusBadRequest)
		}
	}
	if ai != nil && len(ai.PayloadHash) == 64 && !ai.Streaming {
		if hex.EncodeToString(shaHash.Sum(nil)) != strings.ToLower(ai.PayloadHash) {
			return newError("XAmzContentSHA256Mismatch", "The provided 'x-amz-content-sha256' header does not match what was computed.", http.StatusBadRequest)
		}
	}
	meta := objectFromRequest(r, key)
	meta.Size = n
	meta.ETag = hex.EncodeToString(md5Hash.Sum(nil))
	meta.ModTime = time.Now().UTC()
	if err := s.Store.SaveObjectFile(bucket, meta, tmpName); err != nil {
		return err
	}
	w.Header().Set("ETag", quoteETag(meta.ETag))
	w.WriteHeader(http.StatusOK)
	return nil
}

func (s *Server) handleGetObject(w http.ResponseWriter, r *http.Request, bucket, key string, head bool) error {
	f, o, err := s.Store.OpenObject(bucket, key)
	if err != nil {
		if err == store.ErrObjectNotFound {
			return errNoSuchKey
		}
		return err
	}
	defer f.Close()
	h := w.Header()
	h.Set("ETag", quoteETag(o.ETag))
	h.Set("Content-Type", orDefault(o.ContentType, "application/octet-stream"))
	h.Set("Accept-Ranges", "bytes")
	h.Set("Last-Modified", o.ModTime.UTC().Format(http.TimeFormat))
	for k, v := range o.Headers {
		h.Set(k, v)
	}
	for k, v := range o.UserMeta {
		h.Set("x-amz-meta-"+k, v)
	}
	if o.StorageClass != "" {
		h.Set("x-amz-storage-class", o.StorageClass)
	}
	if head {
		h.Set("Content-Length", strconv.FormatInt(o.Size, 10))
		w.WriteHeader(http.StatusOK)
		return nil
	}
	http.ServeContent(w, r, o.Key, o.ModTime, f)
	return nil
}

func (s *Server) handleCopyObject(w http.ResponseWriter, r *http.Request, bucket, key string, ai *authInfo) error {
	src := r.Header.Get("x-amz-copy-source")
	src, _ = url.QueryUnescape(src)
	src = strings.TrimPrefix(src, "/")
	srcBucket, srcKey, _ := strings.Cut(src, "/")
	srcKey, _, _ = strings.Cut(srcKey, "?")
	if srcBucket == "" || srcKey == "" {
		return errInvalidArgument.withMessage("Invalid x-amz-copy-source.")
	}
	f, srcMeta, err := s.Store.OpenObject(srcBucket, srcKey)
	if err != nil {
		return errNoSuchKey
	}
	defer f.Close()

	replace := strings.EqualFold(r.Header.Get("x-amz-metadata-directive"), "REPLACE")
	meta := srcMeta
	meta.Key = key
	if replace {
		meta = objectFromRequest(r, key)
		meta.Key = key
	}
	tmp, err := s.Store.TempFile()
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = removeIfExists(tmpName)
	}()
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), f)
	if err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if r.Header.Get("x-amz-copy-source-range") != "" {
		return errNotImplemented.withMessage("Range copies require multipart copy, which is not supported.")
	}
	meta.Size = n
	meta.ETag = hex.EncodeToString(h.Sum(nil))
	meta.ModTime = time.Now().UTC()
	if err := s.Store.SaveObjectFile(bucket, meta, tmpName); err != nil {
		return err
	}
	w.Header().Set("ETag", quoteETag(meta.ETag))
	return writeXML(w, http.StatusOK, copyObjectResult{LastModified: isoTime(meta.ModTime), ETag: quoteETag(meta.ETag)})
}

func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	if _, err := s.Store.GetBucket(bucket); err != nil {
		return errNoSuchBucket
	}
	_, err := s.Store.DeleteObject(bucket, key)
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) handleDeleteObjects(w http.ResponseWriter, r *http.Request, bucket string) error {
	if _, err := s.Store.GetBucket(bucket); err != nil {
		return errNoSuchBucket
	}
	var req deleteRequest
	if err := xml.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		return errMalformedXML
	}
	if len(req.Objects) == 0 || len(req.Objects) > 1000 {
		return errMalformedXML
	}
	out := deleteResult{NS: s3NS}
	for _, obj := range req.Objects {
		if _, err := s.Store.DeleteObject(bucket, obj.Key); err != nil {
			out.Errors = append(out.Errors, deleteError{Key: obj.Key, Code: "InternalError", Message: err.Error()})
			continue
		}
		out.Deleted = append(out.Deleted, deletedObject{Key: obj.Key})
	}
	return writeXML(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- multipart

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	meta := objectFromRequest(r, key)
	u, err := s.Store.CreateUpload(store.Upload{
		Bucket: bucket, Key: key, ContentType: meta.ContentType,
		StorageClass: "STANDARD", Headers: meta.Headers, UserMeta: meta.UserMeta,
	})
	if err != nil {
		if err == store.ErrBucketNotFound {
			return errNoSuchBucket
		}
		return err
	}
	return writeXML(w, http.StatusOK, initiateMultipartResult{NS: s3NS, Bucket: bucket, Key: key, UploadID: u.UploadID})
}

func (s *Server) handleUploadPart(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values, ai *authInfo) error {
	uploadID := q.Get("uploadId")
	partNum, err := strconv.Atoi(q.Get("partNumber"))
	if err != nil || partNum < 1 || partNum > 10000 {
		return errInvalidArgument.withMessage("Part number must be an integer between 1 and 10000.")
	}
	u, err := s.Store.GetUpload(uploadID)
	if err != nil || u.Bucket != bucket || u.Key != key {
		return errNoSuchUpload
	}
	body, err := decodeBody(r.Body, ai, r.Header)
	if err != nil {
		return err
	}
	tmp, err := s.Store.TempFile()
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = removeIfExists(tmpName)
	}()
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), body)
	if err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	etag := hex.EncodeToString(h.Sum(nil))
	if err := s.Store.PutPart(uploadID, store.Part{
		Number: partNum, Size: n, ETag: etag, ModTime: time.Now().UTC(),
	}, tmpName); err != nil {
		return err
	}
	w.Header().Set("ETag", quoteETag(etag))
	w.WriteHeader(http.StatusOK)
	return nil
}

func (s *Server) handleCompleteUpload(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) error {
	uploadID := q.Get("uploadId")
	u, err := s.Store.GetUpload(uploadID)
	if err != nil || u.Bucket != bucket || u.Key != key {
		return errNoSuchUpload
	}
	var req completeMultipartRequest
	if err := xml.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		return errMalformedXML
	}
	if len(req.Parts) == 0 {
		return errMalformedXML
	}
	storedParts, err := s.Store.ListParts(uploadID)
	if err != nil {
		return errNoSuchUpload
	}
	byNum := map[int]store.Part{}
	for _, p := range storedParts {
		byNum[p.Number] = p
	}
	var chosen []store.Part
	last := 0
	for _, p := range req.Parts {
		sp, ok := byNum[p.PartNumber]
		if !ok {
			return newError("InvalidPart", "One or more of the specified parts could not be found.", http.StatusBadRequest)
		}
		if want := strings.Trim(p.ETag, `"`); want != "" && !strings.EqualFold(want, sp.ETag) {
			return newError("InvalidPart", "The part ETag you provided did not match the stored part.", http.StatusBadRequest)
		}
		if p.PartNumber <= last {
			return newError("InvalidPartOrder", "The list of parts was not in ascending order.", http.StatusBadRequest)
		}
		last = p.PartNumber
		chosen = append(chosen, sp)
	}
	if len(chosen) != len(storedParts) {
		return newError("InvalidPart", "Not all uploaded parts were included in the completion request.", http.StatusBadRequest)
	}
	sum := md5.New()
	for _, p := range chosen {
		raw, err := hex.DecodeString(p.ETag)
		if err != nil {
			return err
		}
		sum.Write(raw)
	}
	etag := fmt.Sprintf("%s-%d", hex.EncodeToString(sum.Sum(nil)), len(chosen))
	meta, err := s.Store.CompleteUpload(u, chosen, etag)
	if err != nil {
		if err == store.ErrUploadNotFound {
			return errNoSuchUpload
		}
		return err
	}
	// The console and S3 API can sit behind HTTPS reverse proxies or CDNs, so
	// the advertised location must follow the scheme the client actually used.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" || proto == "http" {
		scheme = proto
	}
	location := fmt.Sprintf("%s://%s/%s/%s", scheme, r.Host, bucket, url.PathEscape(key))
	w.Header().Set("ETag", quoteETag(meta.ETag))
	return writeXML(w, http.StatusOK, completeMultipartResult{
		NS: s3NS, Location: location, Bucket: bucket, Key: key, ETag: quoteETag(meta.ETag),
	})
}

func (s *Server) handleAbortUpload(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) error {
	u, err := s.Store.GetUpload(q.Get("uploadId"))
	if err != nil || u.Bucket != bucket || u.Key != key {
		return errNoSuchUpload
	}
	if err := s.Store.AbortUpload(u.UploadID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) handleListParts(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) error {
	u, err := s.Store.GetUpload(q.Get("uploadId"))
	if err != nil || u.Bucket != bucket || u.Key != key {
		return errNoSuchUpload
	}
	parts, err := s.Store.ListParts(u.UploadID)
	if err != nil {
		return errNoSuchUpload
	}
	out := listPartsResult{NS: s3NS, Bucket: bucket, Key: key, UploadID: u.UploadID, MaxParts: 1000}
	for _, p := range parts {
		out.Parts = append(out.Parts, xmlPart{
			PartNumber: p.Number, LastModified: isoTime(p.ModTime), ETag: quoteETag(p.ETag), Size: p.Size,
		})
	}
	return writeXML(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- helpers

func writeXML(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	return xml.NewEncoder(w).Encode(v)
}

func quoteETag(s string) string {
	s = strings.Trim(s, `"`)
	if s == "" {
		return ""
	}
	return `"` + s + `"`
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func hmacEqualBytes(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

func isChunkedEncoding(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Content-Encoding")), "aws-chunked") ||
		strings.HasPrefix(r.Header.Get("x-amz-content-sha256"), "STREAMING-")
}

func removeIfExists(name string) error {
	err := os.Remove(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
