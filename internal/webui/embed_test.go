package webui

import (
	"io/fs"
	"strings"
	"testing"
	"unicode/utf8"
)

// The console is embedded at compile time, so a damaged asset ships silently in
// the .fpk. These checks guard the two ways index.html has broken before:
// mojibake from an editor that wrote GBK, and a lost "<" that nested #app inside
// #boot, which hid the whole page once boot.hidden was set.
func TestIndexHTMLIsIntact(t *testing.T) {
	raw, err := fs.ReadFile(Assets, "assets/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if !utf8.Valid(raw) {
		t.Fatal("index.html is not valid UTF-8")
	}
	html := string(raw)
	if strings.ContainsRune(html, '\uFFFD') {
		t.Fatal("index.html contains U+FFFD replacement characters: it was re-encoded from a non-UTF-8 source and its text is corrupt")
	}
	if !strings.Contains(html, `<title>`) {
		t.Fatal("index.html is missing a title")
	}
	// #app must be a sibling of #boot, never its child, or hiding the boot
	// overlay hides the entire console.
	if !strings.Contains(html, "</div>\n  <div id=\"app\"") {
		t.Fatal("index.html structure changed: #app is no longer a direct sibling of #boot")
	}
	if strings.Count(html, `<div id="app"`) != 1 {
		t.Fatal("index.html must contain exactly one #app element")
	}
}

func TestAssetsAreUTF8(t *testing.T) {
	err := fs.WalkDir(Assets, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(Assets, path)
		if err != nil {
			return err
		}
		if !utf8.Valid(raw) {
			t.Errorf("%s is not valid UTF-8", path)
		}
		if strings.ContainsRune(string(raw), '\uFFFD') {
			t.Errorf("%s contains U+FFFD replacement characters", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk assets: %v", err)
	}
}
