// Package node is the host-side worker: it reads the filesystem and runs the
// encoder. Everything here executes on macOS, next to the media.
package node

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// ScanResult is what one library walk produced.
type ScanResult struct {
	Files    []media.Scanned
	Skipped  int // excluded or not a media extension
	Filtered int // matched an exclude pattern
}

// Scan walks a library root. It never follows symlinks, never descends into an
// excluded directory, and skips hidden paths.
func Scan(lib config.Library) (ScanResult, error) {
	var res ScanResult

	// Compile the exclude patterns once.
	globs := make([]*regexp.Regexp, 0, len(lib.Exclude))
	for _, pattern := range lib.Exclude {
		re, err := GlobToRegexp(pattern)
		if err != nil {
			return res, fmt.Errorf("library %q: bad exclude %q: %w", lib.Name, pattern, err)
		}
		globs = append(globs, re)
	}

	err := filepath.WalkDir(lib.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A vanished or unreadable entry is not a reason to abandon the
			// walk; record it and continue.
			res.Skipped++
			return nil
		}
		if path != lib.Root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			res.Skipped++
			return nil
		}
		if matchedAny(globs, path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			res.Filtered++
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !hasExtension(lib.Extensions, d.Name()) {
			res.Skipped++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			res.Skipped++
			return nil
		}
		res.Files = append(res.Files, media.Scanned{
			Path:    path,
			Size:    info.Size(),
			MtimeNS: info.ModTime().UnixNano(),
		})
		return nil
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

func matchedAny(globs []*regexp.Regexp, path string) bool {
	for _, re := range globs {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

func hasExtension(exts []string, name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range exts {
		if strings.EqualFold(e, ext) {
			return true
		}
	}
	return false
}

// GlobToRegexp supports the three constructs config files actually use:
// "**" (any number of directories), "*" (within one segment) and "?".
func GlobToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// Exists reports whether a path is present, used before a job starts.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
