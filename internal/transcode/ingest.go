package transcode

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

// StageError is a failure with the code the operator will see. Stages return it
// so the caller does not have to reclassify what went wrong.
type StageError struct {
	Code    string
	Message string
}

func (e *StageError) Error() string { return e.Message }

// edgeBytes is how much of each end of a file the ingest check hashes. A full
// hash of a 40 GB film costs minutes and would defeat the point of staging;
// the head and tail catch truncation, a partial copy and most corruption,
// which is what an interrupted copy actually produces.
const edgeBytes = 1 << 20

// EdgeHashes returns the digests of the first and last megabyte.
func EdgeHashes(path string) (head, tail [sha256.Size]byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return head, tail, err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return head, tail, err
	}

	buf := make([]byte, edgeBytes)
	n, readErr := io.ReadFull(f, buf)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return head, tail, readErr
	}
	h := sha256.New()
	_, _ = h.Write(buf[:n])
	copy(head[:], h.Sum(nil))

	tailStart := fi.Size() - edgeBytes
	if tailStart < 0 {
		tailStart = 0
	}
	if _, err := f.Seek(tailStart, io.SeekStart); err != nil {
		return head, tail, err
	}
	n, readErr = io.ReadFull(f, buf)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return head, tail, readErr
	}
	h.Reset()
	_, _ = h.Write(buf[:n])
	copy(tail[:], h.Sum(nil))

	return head, tail, nil
}

// SameEdges reports whether two files share their size and both edge digests.
func SameEdges(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if ai.Size() != bi.Size() {
		return false, nil
	}
	ah, at, err := EdgeHashes(a)
	if err != nil {
		return false, err
	}
	bh, bt, err := EdgeHashes(b)
	if err != nil {
		return false, err
	}
	return ah == bh && at == bt, nil
}

// FreeGB reports the space available on the volume holding path, which is what
// a caller needs to decide whether to start work at all.
func FreeGB(path string) (float64, error) { return freeGB(path) }

// SourcePath is where a job's ingested copy lives.
func SourcePath(cfg *config.Config, id int64) string {
	return filepath.Join(cfg.Paths.Scratch, fmt.Sprintf("%d.src.mkv", id))
}

// OutputPath is where a job's encoded result is written before the swap.
func OutputPath(cfg *config.Config, id int64) string {
	return filepath.Join(cfg.Paths.Scratch, fmt.Sprintf("%d.out.mkv", id))
}

// EnsureSource makes sure a verified copy of the source exists in scratch.
//
// It is idempotent on purpose: a prefetcher may have copied the file minutes
// ago, and the encoder should check that copy rather than copy it again. The
// check is the same either way — size and both edge digests against the
// original — so "reuse" is never a weaker guarantee than "copy".
//
// It reports whether it actually copied, which is what the caller logs.
func EnsureSource(ctx context.Context, cfg *config.Config, id int64, source string, logf func(stage, msg string)) (path string, copied bool, err error) {
	if logf == nil {
		logf = func(string, string) {}
	}
	src := SourcePath(cfg, id)

	if _, statErr := os.Stat(src); statErr == nil {
		same, cmpErr := SameEdges(source, src)
		if cmpErr == nil && same {
			logf("ingest", "reusing the staged copy")
			return src, false, nil
		}
		// A copy that does not match is worse than none: it would encode the
		// wrong bytes. Drop it and start again.
		_ = os.Remove(src)
	}

	if _, err := os.Stat(source); err != nil {
		return "", false, &StageError{Code: "E_SOURCE_MISSING", Message: err.Error()}
	}

	logf("ingest", "copying to scratch")
	// Copy to a temporary name and rename: a half-written .src.mkv would look
	// like a reusable copy to the next attempt.
	tmp := src + ".part"
	if err := CopyFile(source, tmp, nil); err != nil {
		_ = os.Remove(tmp)
		return "", false, &StageError{Code: "E_SOURCE_MISSING", Message: "ingest: " + err.Error()}
	}
	if err := os.Rename(tmp, src); err != nil {
		_ = os.Remove(tmp)
		return "", false, &StageError{Code: "E_SOURCE_MISSING", Message: "ingest: " + err.Error()}
	}

	same, err := SameEdges(source, src)
	if err != nil {
		return "", false, &StageError{Code: "E_INGEST_VERIFY", Message: err.Error()}
	}
	if !same {
		_ = os.Remove(src)
		return "", false, &StageError{
			Code:    "E_SOURCE_CHANGED",
			Message: "the source changed while it was being copied, or the copy is damaged",
		}
	}
	return src, true, nil
}
