package store

import "time"

// Bucket describes an S3 bucket. Policies are intentionally small: private or
// public-read. Everything else is managed through fnOS gateway authentication.
//
// RootPath is empty for buckets stored inside the app data volume in a
// content-addressed layout. When set, it points at an authorised fnOS folder and
// object keys map to real files below it.
type Bucket struct {
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Policy   string    `json:"policy"` // private | public-read
	RootPath string    `json:"rootPath,omitempty"`
}

type Object struct {
	Key          string            `json:"key"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag"`
	ModTime      time.Time         `json:"modTime"`
	ContentType  string            `json:"contentType"`
	StorageClass string            `json:"storageClass"`
	Headers      map[string]string `json:"headers,omitempty"`
	UserMeta     map[string]string `json:"userMeta,omitempty"`
}

type Part struct {
	Number  int       `json:"number"`
	Size    int64     `json:"size"`
	ETag    string    `json:"etag"`
	ModTime time.Time `json:"modTime"`
}

type Upload struct {
	Bucket       string            `json:"bucket"`
	Key          string            `json:"key"`
	UploadID     string            `json:"uploadId"`
	Created      time.Time         `json:"created"`
	ContentType  string            `json:"contentType"`
	StorageClass string            `json:"storageClass"`
	Headers      map[string]string `json:"headers,omitempty"`
	UserMeta     map[string]string `json:"userMeta,omitempty"`
}

type APIKey struct {
	AccessKey string     `json:"accessKey"`
	SecretKey string     `json:"secretKey"`
	Label     string     `json:"label"`
	Created   time.Time  `json:"created"`
	Enabled   bool       `json:"enabled"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
	// Buckets restricts the key to the listed buckets. An empty list means all
	// buckets, which keeps keys created before scoped keys existed privileged.
	Buckets  []string `json:"buckets,omitempty"`
	ReadOnly bool     `json:"readOnly,omitempty"`
}

// AllowsBucket reports whether the key may access bucket. An empty scope grants
// every bucket.
func (k APIKey) AllowsBucket(bucket string) bool {
	if len(k.Buckets) == 0 {
		return true
	}
	for _, b := range k.Buckets {
		if b == bucket {
			return true
		}
	}
	return false
}

type Settings struct {
	CORSOrigins    []string `json:"corsOrigins"`
	MaxUploadSize  int64    `json:"maxUploadSize"`
	PublicEndpoint string   `json:"publicEndpoint"`
}

func DefaultSettings() Settings {
	return Settings{
		CORSOrigins:   []string{"*"},
		MaxUploadSize: 0, // unlimited
	}
}

type ListResult struct {
	Objects        []Object
	CommonPrefixes []string
	IsTruncated    bool
	NextMarker     string // last returned key or common prefix (plain text)
	KeyCount       int
}

type Stats struct {
	Buckets      int
	Objects      int64
	Size         int64
	Uploads      int
	StorageFree  uint64
	StorageTotal uint64
}
