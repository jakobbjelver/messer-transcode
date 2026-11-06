package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Progress is one parsed block of ffmpeg's -progress stream.
type Progress struct {
	Frame     int
	OutTimeUS int64
	Speed     float64
}

// RunError carries the stable code and the tail of ffmpeg's stderr.
type RunError struct {
	Code   string
	Stderr string
	Err    error
}

func (e *RunError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Code, msg)
}

func (e *RunError) Unwrap() error { return e.Err }

// Run executes ffmpeg and reports progress. Cancelling ctx kills the process;
// the caller decides whether that is a pause, a shutdown or a failure.
func Run(ctx context.Context, args []string, onProgress func(Progress)) error {
	_, err := RunTail(ctx, args, onProgress)
	return err
}

// RunTail is Run, plus the tail of stderr even on success. When a process exits
// zero but leaves no output, that tail is the only evidence there is.
func RunTail(ctx context.Context, args []string, onProgress func(Progress)) (string, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", &RunError{Code: "E_ENCODE_FFMPEG", Err: err}
	}
	tail := &tailWriter{max: 8192}
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		return "", &RunError{Code: "E_ENCODE_FFMPEG", Err: err}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "frame":
			if n, err := strconv.Atoi(value); err == nil {
				report(onProgress, Progress{Frame: n})
			}
		case "out_time_us":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				report(onProgress, Progress{OutTimeUS: n})
			}
		case "speed":
			report(onProgress, Progress{Speed: parseSpeed(value)})
		}
	}

	if err := cmd.Wait(); err != nil {
		code := "E_ENCODE_FFMPEG"
		if ctx.Err() != nil {
			code = "E_ENCODE_CANCELLED"
		}
		return tail.String(), &RunError{Code: code, Stderr: tail.String(), Err: err}
	}
	if ctx.Err() != nil {
		return tail.String(), &RunError{Code: "E_ENCODE_CANCELLED", Stderr: tail.String(), Err: ctx.Err()}
	}
	return tail.String(), nil
}

// report sends a partial update; callers merge the fields they care about.
func report(onProgress func(Progress), p Progress) {
	if onProgress != nil {
		onProgress(p)
	}
}

// tailWriter keeps only the last max bytes of stderr: ffmpeg can be verbose and
// the useful part is always at the end.
type tailWriter struct {
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return string(w.buf) }

// Available reports whether a binary is on PATH and runnable.
func Available(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
