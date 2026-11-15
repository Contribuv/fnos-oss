// Command fixfpk normalizes file modes inside a fnOS .fpk package.
//
// fnpack reads modes from the host filesystem. On Windows every entry ends up
// with 0666, which leaves lifecycle scripts and the server binary without an
// execute bit and marks all packaged files world-writable. This tool rewrites
// the tar headers (including the nested app.tgz) so the published package
// carries the modes a Linux installation expects.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	dirMode     = 0o755
	execMode    = 0o755
	regularMode = 0o644
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fixfpk <package.fpk> [...]")
		os.Exit(2)
	}
	for _, name := range os.Args[1:] {
		if err := fixFile(name); err != nil {
			fmt.Fprintf(os.Stderr, "fix %s: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Println("fixed", name)
	}
}

func fixFile(name string) error {
	src, err := os.Open(name)
	if err != nil {
		return err
	}

	zr, err := gzip.NewReader(src)
	if err != nil {
		src.Close()
		return fmt.Errorf("读取 gzip: %w", err)
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	err = rewriteOuter(zr, zw)
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = zr.Close()
	}
	if closeErr := src.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, name); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// rewriteOuter copies the package tar, fixing header modes and rewriting the
// embedded application archive.
func rewriteOuter(r io.Reader, w io.Writer) error {
	tr := tar.NewReader(r)
	tw := tar.NewWriter(w)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		h := *hdr
		h.Mode = outerMode(h.Name, &h)
		if path.Clean(strings.TrimPrefix(h.Name, "./")) == "app.tgz" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			fixed, err := fixAppTgz(data)
			if err != nil {
				return fmt.Errorf("rewrite app.tgz: %w", err)
			}
			h.Size = int64(len(fixed))
			if err := tw.WriteHeader(&h); err != nil {
				return err
			}
			if _, err := tw.Write(fixed); err != nil {
				return err
			}
			continue
		}
		if err := tw.WriteHeader(&h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
	return tw.Close()
}

func outerMode(name string, hdr *tar.Header) int64 {
	if hdr.Typeflag == tar.TypeDir {
		return dirMode
	}
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if strings.HasPrefix(clean, "cmd/") {
		return execMode
	}
	if clean == "app/fnososs" {
		return execMode
	}
	return regularMode
}

func innerMode(name string, hdr *tar.Header) int64 {
	if hdr.Typeflag == tar.TypeDir {
		return dirMode
	}
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if clean == "fnososs" {
		return execMode
	}
	return regularMode
}

// fixAppTgz re-packs the gzipped application archive with normalized modes.
func fixAppTgz(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	tr := tar.NewReader(zr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		h := *hdr
		h.Mode = innerMode(h.Name, &h)
		if err := tw.WriteHeader(&h); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
