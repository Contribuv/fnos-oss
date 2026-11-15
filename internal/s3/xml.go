package s3

import (
	"encoding/xml"
	"time"
)

const s3NS = "http://s3.amazonaws.com/doc/2006-03-01/"

func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

type xmlOwner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

func owner() xmlOwner { return xmlOwner{ID: "fnos", DisplayName: "fnos"} }

type xmlBucket struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type listAllBucketsResult struct {
	XMLName xml.Name    `xml:"ListAllMyBucketsResult"`
	NS      string      `xml:"xmlns,attr"`
	Owner   xmlOwner    `xml:"Owner"`
	Buckets []xmlBucket `xml:"Buckets>Bucket"`
}

type xmlObject struct {
	Key          string    `xml:"Key"`
	LastModified string    `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
	Owner        *xmlOwner `xml:"Owner,omitempty"`
}

type xmlCommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type listBucketResult struct {
	XMLName               xml.Name          `xml:"ListBucketResult"`
	NS                    string            `xml:"xmlns,attr"`
	Name                  string            `xml:"Name"`
	Prefix                string            `xml:"Prefix"`
	Delimiter             string            `xml:"Delimiter,omitempty"`
	Marker                string            `xml:"Marker,omitempty"`
	NextMarker            string            `xml:"NextMarker,omitempty"`
	MaxKeys               int               `xml:"MaxKeys"`
	KeyCount              int               `xml:"KeyCount,omitempty"`
	IsTruncated           bool              `xml:"IsTruncated"`
	ContinuationToken     string            `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string            `xml:"NextContinuationToken,omitempty"`
	StartAfter            string            `xml:"StartAfter,omitempty"`
	Contents              []xmlObject       `xml:"Contents"`
	CommonPrefixes        []xmlCommonPrefix `xml:"CommonPrefixes"`
}

type locationConstraint struct {
	XMLName xml.Name `xml:"LocationConstraint"`
	NS      string   `xml:"xmlns,attr"`
	Value   string   `xml:",chardata"`
}

type versioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	NS      string   `xml:"xmlns,attr"`
	Status  string   `xml:"Status,omitempty"`
}

type copyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

type copyPartResult struct {
	XMLName      xml.Name `xml:"CopyPartResult"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

type initiateMultipartResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	NS       string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type xmlCompletedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeMultipartRequest struct {
	XMLName xml.Name           `xml:"CompleteMultipartUpload"`
	Parts   []xmlCompletedPart `xml:"Part"`
}

type completeMultipartResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	NS       string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type xmlPart struct {
	PartNumber   int    `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}

type listPartsResult struct {
	XMLName              xml.Name  `xml:"ListPartsResult"`
	NS                   string    `xml:"xmlns,attr"`
	Bucket               string    `xml:"Bucket"`
	Key                  string    `xml:"Key"`
	UploadID             string    `xml:"UploadId"`
	PartNumberMarker     int       `xml:"PartNumberMarker"`
	NextPartNumberMarker int       `xml:"NextPartNumberMarker"`
	MaxParts             int       `xml:"MaxParts"`
	IsTruncated          bool      `xml:"IsTruncated"`
	Parts                []xmlPart `xml:"Part"`
}

type xmlUpload struct {
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	Initiated    string    `xml:"Initiated"`
	StorageClass string    `xml:"StorageClass"`
	Owner        *xmlOwner `xml:"Owner,omitempty"`
}

type listUploadsResult struct {
	XMLName            xml.Name    `xml:"ListMultipartUploadsResult"`
	NS                 string      `xml:"xmlns,attr"`
	Bucket             string      `xml:"Bucket"`
	KeyMarker          string      `xml:"KeyMarker"`
	UploadIDMarker     string      `xml:"UploadIdMarker"`
	NextKeyMarker      string      `xml:"NextKeyMarker"`
	NextUploadIDMarker string      `xml:"NextUploadIdMarker"`
	MaxUploads         int         `xml:"MaxUploads"`
	IsTruncated        bool        `xml:"IsTruncated"`
	Uploads            []xmlUpload `xml:"Upload"`
}

type deletedObject struct {
	Key string `xml:"Key"`
}

type deleteError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

type deleteResult struct {
	XMLName xml.Name        `xml:"DeleteResult"`
	NS      string          `xml:"xmlns,attr"`
	Deleted []deletedObject `xml:"Deleted"`
	Errors  []deleteError   `xml:"Error"`
}

type deleteRequest struct {
	XMLName xml.Name `xml:"Delete"`
	Objects []struct {
		Key       string `xml:"Key"`
		VersionID string `xml:"VersionId"`
	} `xml:"Object"`
	Quiet bool `xml:"Quiet"`
}
