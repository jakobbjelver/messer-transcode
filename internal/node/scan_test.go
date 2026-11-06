package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

func TestGlobToRegexp(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/Sample/**", "/Volumes/Media/Library/Movies/X (2020)/Sample/x.mkv", true},
		{"**/Sample/**", "/Volumes/Media/Library/Movies/Sample/x.mkv", true},
		{"**/Sample/**", "/Volumes/Media/Library/Movies/X (2020)/Feature/x.mkv", false},
		{"**/*.sample.mkv", "/a/b/movie.sample.mkv", true},
		{"**/*.sample.mkv", "/a/b/movie.mkv", false},
		{"**/.*", "/a/.DS_Store", true},
		{"**/.*", "/a/b.mkv", false},
		{"*.mkv", "/a/b.mkv", false}, // * never crosses a separator
	} {
		re, err := GlobToRegexp(tc.pattern)
		if err != nil {
			t.Fatalf("%q: %v", tc.pattern, err)
		}
		if got := re.MatchString(tc.path); got != tc.want {
			t.Errorf("%q vs %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("Film (2020)/film.mkv")
	mustWrite("Film (2020)/notes.txt")
	mustWrite("Film (2020)/Sample/sample.mkv")
	mustWrite("Show/Season 01/ep01.MKV")
	mustWrite(".hidden/secret.mkv")
	mustWrite("loose.mp4")

	res, err := Scan(config.Library{
		Name:       "test",
		Root:       root,
		Extensions: []string{".mkv", ".mp4"},
		Exclude:    []string{"**/Sample/**", "**/.*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, f := range res.Files {
		rel, err := filepath.Rel(root, f.Path)
		if err != nil {
			t.Fatal(err)
		}
		got[rel] = true
		if f.Size == 0 {
			t.Errorf("%s has no size", rel)
		}
		if f.MtimeNS == 0 {
			t.Errorf("%s has no mtime", rel)
		}
	}

	for _, want := range []string{
		"Film (2020)/film.mkv",
		"Show/Season 01/ep01.MKV",
		"loose.mp4",
	} {
		if !got[want] {
			t.Errorf("expected %q in the scan, got %v", want, keys(got))
		}
	}
	for _, unwanted := range []string{
		"Film (2020)/notes.txt",
		"Film (2020)/Sample/sample.mkv",
		".hidden/secret.mkv",
	} {
		if got[unwanted] {
			t.Errorf("%q should have been skipped", unwanted)
		}
	}
	if res.Filtered != 1 {
		t.Errorf("filtered = %d, want 1 (the Sample directory)", res.Filtered)
	}
}

func TestScanMissingRoot(t *testing.T) {
	res, err := Scan(config.Library{
		Name:       "gone",
		Root:       filepath.Join(t.TempDir(), "does-not-exist"),
		Extensions: []string{".mkv"},
	})
	// WalkDir reports the root error to the callback, which we count and skip,
	// so the walk finishes with no files rather than panicking.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Files) != 0 {
		t.Errorf("expected no files, got %d", len(res.Files))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
