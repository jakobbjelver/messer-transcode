package transcode

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// CopyFile copies src to dst and fsyncs the result. onBytes may be nil.
func CopyFile(src, dst string, onBytes func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	var written int64
	buf := make([]byte, 4<<20)
	for {
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				_ = out.Close()
				return err
			}
			written += int64(n)
			if onBytes != nil {
				onBytes(written)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = out.Close()
			return readErr
		}
	}

	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("fsync %s: %w", dst, err)
	}
	return out.Close()
}

// SameSize reports whether two files have identical byte counts, which is the
// cheapest useful proof that a copy completed.
func SameSize(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return ia.Size() == ib.Size(), nil
}

// Quarantine keeps the original safe before anything replaces it.
//
// A hardlink is used when the two paths share a filesystem: it costs nothing,
// is atomic, and the original's bytes stay alive under the quarantine name.
// Across filesystems it falls back to a full copy. Both paths leave the
// original in place, so a failure here never loses data.
func Quarantine(original, libraryRoot, quarantineRoot string, at time.Time) (string, error) {
	rel, err := filepath.Rel(libraryRoot, original)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(original)
	}
	dir := filepath.Join(quarantineRoot, at.Format("2006-01-02"), filepath.Dir(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	base := filepath.Base(rel)
	for attempt := 0; attempt < 100; attempt++ {
		dst := filepath.Join(dir, base)
		if attempt > 0 {
			dst = filepath.Join(dir, fmt.Sprintf("%s.%d", base, attempt))
		}
		if _, err := os.Stat(dst); err == nil {
			continue // already quarantined today; try the next suffix
		}
		if err := os.Link(original, dst); err == nil {
			return dst, nil
		}
		if err := CopyFile(original, dst, nil); err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", fmt.Errorf("could not find a free quarantine name for %s", base)
}

// Replace atomically swaps tmp into final, after making sure the bytes are on
// disk. Both paths must be in the same directory for the rename to be atomic.
func Replace(tmp, final string) error {
	f, err := os.OpenFile(tmp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmp, final, err)
	}
	syncDir(filepath.Dir(final))
	return nil
}

// syncDir makes the rename itself durable. Best effort: not every filesystem
// allows opening a directory for this.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// LockFile takes an exclusive advisory lock so two workers can never touch the
// same file. The returned function releases it.
func LockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("locked by another worker: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
