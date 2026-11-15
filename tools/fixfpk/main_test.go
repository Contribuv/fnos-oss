package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type entry struct {
	name string
	mode int64
	dir  bool
	data string
}

func buildTar(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.data))}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		} else {
			hdr.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if !e.dir {
			if _, err := tw.Write([]byte(e.data)); err != nil {
				t.Fatalf("write %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func buildGz(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func readTar(t *testing.T, data []byte) map[string]*tar.Header {
	t.Helper()
	out := map[string]*tar.Header{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		h := *hdr
		out[hdr.Name] = &h
	}
	return out
}

func TestRewriteOuterModes(t *testing.T) {
	inner := buildTar(t, []entry{
		{name: "fnososs", mode: 0o666, data: "binary"},
		{name: "ui/", mode: 0o777, dir: true},
		{name: "ui/config", mode: 0o666, data: "{}"},
		{name: "ui/images/icon.png", mode: 0o666, data: "png"},
	})
	appTgz := buildGz(t, inner)

	outer := buildTar(t, []entry{
		{name: "cmd/", mode: 0o777, dir: true},
		{name: "cmd/main", mode: 0o666, data: "#!/bin/bash"},
		{name: "cmd/install_callback", mode: 0o666, data: "#!/bin/bash"},
		{name: "manifest", mode: 0o666, data: "appname=fnos-oss"},
		{name: "app.tgz", mode: 0o666, data: string(appTgz)},
	})

	var out bytes.Buffer
	if err := rewriteOuter(bytes.NewReader(outer), &out); err != nil {
		t.Fatalf("rewriteOuter: %v", err)
	}

	pkg := readTar(t, out.Bytes())
	want := map[string]int64{
		"cmd/":                 dirMode,
		"cmd/main":             execMode,
		"cmd/install_callback": execMode,
		"manifest":             regularMode,
		"app.tgz":              regularMode,
	}
	for name, mode := range want {
		hdr, ok := pkg[name]
		if !ok {
			t.Fatalf("missing entry %q", name)
		}
		if hdr.Mode != mode {
			t.Errorf("%s mode = %#o, want %#o", name, hdr.Mode, mode)
		}
	}

	zr, err := gzip.NewReader(bytes.NewReader(mustData(t, out.Bytes(), "app.tgz")))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	innerOut := readTar(t, mustReadAll(t, zr))
	zr.Close()

	innerWant := map[string]int64{
		"fnososs":            execMode,
		"ui/":                dirMode,
		"ui/config":          regularMode,
		"ui/images/icon.png": regularMode,
	}
	for name, mode := range innerWant {
		hdr, ok := innerOut[name]
		if !ok {
			t.Fatalf("missing inner entry %q", name)
		}
		if hdr.Mode != mode {
			t.Errorf("inner %s mode = %#o, want %#o", name, hdr.Mode, mode)
		}
	}
}

func TestFixFile(t *testing.T) {
	inner := buildTar(t, []entry{
		{name: "fnososs", mode: 0o666, data: "binary"},
		{name: "ui/config", mode: 0o666, data: "{}"},
	})
	pkgTar := buildTar(t, []entry{
		{name: "cmd/main", mode: 0o666, data: "#!/bin/bash"},
		{name: "manifest", mode: 0o666, data: "appname=fnos-oss"},
		{name: "app.tgz", mode: 0o666, data: string(buildGz(t, inner))},
	})

	dir := t.TempDir()
	pkg := filepath.Join(dir, "test.fpk")
	if err := os.WriteFile(pkg, buildGz(t, pkgTar), 0o644); err != nil {
		t.Fatalf("write package: %v", err)
	}
	if err := fixFile(pkg); err != nil {
		t.Fatalf("fixFile: %v", err)
	}

	data, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	pkgBytes := mustReadAll(t, zr)
	zr.Close()

	pkgEntries := readTar(t, pkgBytes)
	if got := pkgEntries["cmd/main"].Mode; got != execMode {
		t.Errorf("cmd/main mode = %#o, want %#o", got, execMode)
	}
	if got := pkgEntries["manifest"].Mode; got != regularMode {
		t.Errorf("manifest mode = %#o, want %#o", got, regularMode)
	}
	if got := pkgEntries["app.tgz"].Mode; got != regularMode {
		t.Errorf("app.tgz mode = %#o, want %#o", got, regularMode)
	}

	innerZr, err := gzip.NewReader(bytes.NewReader(mustData(t, pkgBytes, "app.tgz")))
	if err != nil {
		t.Fatalf("inner gzip: %v", err)
	}
	innerEntries := readTar(t, mustReadAll(t, innerZr))
	innerZr.Close()
	if got := innerEntries["fnososs"].Mode; got != execMode {
		t.Errorf("fnososs mode = %#o, want %#o", got, execMode)
	}
}

func mustData(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("entry %q not found", name)
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		if hdr.Name == name {
			return mustReadAll(t, tr)
		}
	}
}

func mustReadAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	return data
}
