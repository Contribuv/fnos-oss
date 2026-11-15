package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"

	"fnososs/internal/store"
)

type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func newError(code, message string, status int) *APIError {
	return &APIError{Code: code, Message: message, Status: status}
}

var (
	errAccessDenied       = newError("AccessDenied", "Access Denied.", http.StatusForbidden)
	errSignatureMismatch  = newError("SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided.", http.StatusForbidden)
	errInvalidAccessKey   = newError("InvalidAccessKeyId", "The AWS Access Key Id you provided does not exist in our records.", http.StatusForbidden)
	errMalformedAuth      = newError("AuthorizationHeaderMalformed", "The authorization header is malformed.", http.StatusBadRequest)
	errRequestTimeTooSkew = newError("RequestTimeTooSkewed", "The difference between the request time and the current time is too large.", http.StatusForbidden)
	errNoSuchBucket       = newError("NoSuchBucket", "The specified bucket does not exist.", http.StatusNotFound)
	errNoSuchKey          = newError("NoSuchKey", "The specified key does not exist.", http.StatusNotFound)
	errNoSuchUpload       = newError("NoSuchUpload", "The specified multipart upload does not exist.", http.StatusNotFound)
	errBucketNotEmpty     = newError("BucketNotEmpty", "The bucket you tried to delete is not empty.", http.StatusConflict)
	errBucketExists       = newError("BucketAlreadyOwnedByYou", "Your previous request to create the named bucket succeeded and you already own it.", http.StatusConflict)
	errInvalidBucketName  = newError("InvalidBucketName", "The specified bucket is not valid.", http.StatusBadRequest)
	errInvalidArgument    = newError("InvalidArgument", "Invalid argument.", http.StatusBadRequest)
	errMalformedXML       = newError("MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema.", http.StatusBadRequest)
	errMethodNotAllowed   = newError("MethodNotAllowed", "The specified method is not allowed against this resource.", http.StatusMethodNotAllowed)
	errNotImplemented     = newError("NotImplemented", "A header or query you provided implies functionality that is not implemented.", http.StatusNotImplemented)
	errInternal           = newError("InternalError", "We encountered an internal error. Please try again.", http.StatusInternalServerError)
	errIncompleteBody     = newError("IncompleteBody", "You did not provide the number of bytes specified by the Content-Length header.", http.StatusBadRequest)
	errEntityTooLarge     = newError("EntityTooLarge", "Your proposed upload exceeds the maximum allowed object size.", http.StatusBadRequest)
	errKeyConflict        = newError("KeyConflict", "The object key conflicts with an existing file or folder in the bucket directory.", http.StatusConflict)
)

type errorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId"`
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr, ok := err.(*APIError)
	if !ok {
		if mapped := mapStoreError(err); mapped != nil {
			apiErr = mapped
		} else {
			apiErr = errInternal
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(apiErr.Status)
	body := errorResponse{
		Code:      apiErr.Code,
		Message:   apiErr.Message,
		Resource:  r.URL.Path,
		RequestID: requestID(r),
	}
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(body)
}

func (e *APIError) withMessage(msg string) *APIError {
	c := *e
	c.Message = msg
	return &c
}

func statusOK(status int) bool { return status >= 200 && status < 300 }

// mapStoreError translates store sentinels into S3 errors. It returns nil for
// anything else, which the caller reports as an internal error.
func mapStoreError(err error) *APIError {
	switch {
	case errors.Is(err, store.ErrInvalidKey):
		return errInvalidArgument.withMessage("The object key cannot be mapped to a file: empty, '.', '..' and trailing '/' segments are not supported by this bucket.")
	case errors.Is(err, store.ErrKeyConflict):
		return errKeyConflict
	}
	return nil
}

func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*APIError); ok {
		return err
	}
	if mapped := mapStoreError(err); mapped != nil {
		return mapped
	}
	return newError("InternalError", fmt.Sprintf("Internal error: %v", err), http.StatusInternalServerError)
}
