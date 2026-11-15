package s3

import (
	"bufio"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// chunkedReader decodes the aws-chunked transfer encoding used by AWS SDKs.
// Chunk signatures are verified when the request was signed with the streaming
// payload scheme; unsigned trailer requests are decoded and their trailer is
// consumed but cannot be authenticated at the chunk level.
type chunkedReader struct {
	br         *bufio.Reader
	signingKey []byte
	scope      string
	amzDate    string
	prevSig    string
	verify     bool

	remaining int64 // decoded bytes left (0 = unknown)
	chunk     []byte
	off       int
	done      bool
	err       error
}

func newChunkedReader(r io.Reader, a *authInfo, decodedLen int64, verify bool) *chunkedReader {
	return &chunkedReader{
		br:         bufio.NewReaderSize(r, 64*1024),
		signingKey: a.SigningKey,
		scope:      a.Scope,
		amzDate:    a.AmzDate,
		prevSig:    "",
		verify:     verify,
		remaining:  decodedLen,
	}
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	for c.off >= len(c.chunk) {
		if c.done {
			return 0, io.EOF
		}
		if err := c.nextChunk(); err != nil {
			c.err = err
			return 0, err
		}
	}
	n := copy(p, c.chunk[c.off:])
	c.off += n
	return n, nil
}

func (c *chunkedReader) nextChunk() error {
	line, err := c.readLine()
	if err != nil {
		return err
	}
	if line == "" {
		return io.ErrUnexpectedEOF
	}
	sizePart, rest, _ := strings.Cut(line, ";")
	size, err := strconv.ParseInt(strings.TrimSpace(sizePart), 16, 64)
	if err != nil || size < 0 || size > 64<<20 {
		return fmt.Errorf("invalid aws-chunked size")
	}
	sig := ""
	if strings.HasPrefix(rest, "chunk-signature=") {
		sig = strings.TrimSpace(strings.TrimPrefix(rest, "chunk-signature="))
	}

	if size == 0 {
		// Consume trailer headers (e.g. x-amz-checksum-crc32) up to the blank
		// line that terminates the body.
		for {
			l, err := c.readLine()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
			if l == "" {
				break
			}
		}
		c.done = true
		return nil
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(c.br, data); err != nil {
		return io.ErrUnexpectedEOF
	}
	trailer, err := c.readLine()
	if err != nil {
		return err
	}
	if trailer != "" {
		return fmt.Errorf("invalid chunk terminator")
	}
	if c.verify {
		if sig == "" {
			return fmt.Errorf("missing chunk signature")
		}
		stringToSign := strings.Join([]string{
			"AWS4-HMAC-SHA256-PAYLOAD",
			c.amzDate,
			c.scope,
			c.prevSig,
			emptySHA256,
			sha256Hex(data),
		}, "\n")
		want := hex.EncodeToString(hmacSHA256(c.signingKey, stringToSign))
		if !hmac.Equal([]byte(want), []byte(strings.ToLower(sig))) {
			return fmt.Errorf("chunk signature mismatch")
		}
		c.prevSig = strings.ToLower(sig)
	}
	if c.remaining > 0 {
		if int64(len(data)) > c.remaining {
			return fmt.Errorf("aws-chunked body exceeds declared length")
		}
		c.remaining -= int64(len(data))
	}
	c.chunk = data
	c.off = 0
	return nil
}

func (c *chunkedReader) readLine() (string, error) {
	line, err := c.br.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// decodeBody returns the request body with aws-chunked encoding removed when
// needed and enforces the decoded content length.
func decodeBody(r io.Reader, a *authInfo, header httpHeader) (io.Reader, error) {
	decodedLen := int64(-1)
	if v := header.Get("X-Amz-Decoded-Content-Length"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return nil, errMalformedAuth.withMessage("Invalid X-Amz-Decoded-Content-Length.")
		}
		decodedLen = n
	}
	if a != nil && a.Streaming {
		verify := a.PayloadHash == "STREAMING-AWS4-HMAC-SHA256-PAYLOAD" || a.PayloadHash == "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
		return newChunkedReader(r, a, decodedLen, verify), nil
	}
	if decodedLen >= 0 {
		return io.LimitReader(r, decodedLen), nil
	}
	return r, nil
}

// httpHeader lets both http.Request and tests provide header access.
type httpHeader interface {
	Get(string) string
}
