package transcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/ffmpeg"
	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/planner"
)

// Task is one file to process.
type Task struct {
	ID          int64
	Path        string
	LibraryRoot string
	Decision    string // encode | remux
	TargetKbps  int
}

// Reporter is where a job's progress and outcome go. The CLI writes to the
// local database; the node writes to the orchestrator over HTTP. The pipeline
// does not care which — it only knows that something is listening.
type Reporter interface {
	SetState(ctx context.Context, id int64, state string) error
	SetProgress(ctx context.Context, id int64, fraction, speed float64) error
	Event(ctx context.Context, level, code string, fileID int64, message, data string) error
	Skip(ctx context.Context, id int64, code, reason string) error
	Finish(ctx context.Context, id int64, before, after int64, vmaf float64) error
	Fail(ctx context.Context, id int64, code, detail string, maxAttempts int) (state string, err error)
}

// Options configure a run.
type Options struct {
	Config   *config.Config
	Reporter Reporter
	Log      func(Event)
	DryRun   bool // do everything except the final swap
}

// Event is progress worth showing a human or an agent.
type Event struct {
	FileID   int64   `json:"file_id"`
	Path     string  `json:"path,omitempty"`
	Stage    string  `json:"stage"`
	Code     string  `json:"code,omitempty"`
	Message  string  `json:"message"`
	Fraction float64 `json:"fraction,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
}

// Result is what one file produced.
type Result struct {
	BytesBefore int64  `json:"bytes_before"`
	BytesAfter  int64  `json:"bytes_after"`
	Quarantine  string `json:"quarantine,omitempty"`
	Seconds     int64  `json:"seconds"`
	Verdict     Verdict
}

// Process runs one file through ingest → encode → verify → replace.
//
// The ordering matters. The quarantine step runs before the swap and never
// removes anything on its own; the swap is a single rename inside the
// destination directory. If the process dies anywhere before the rename, the
// original is untouched and the scratch files are garbage to be cleaned up. If
// it dies after the quarantine step but before the rename, both copies exist.
func Process(ctx context.Context, opts Options, t Task) (Result, error) {
	res, commit, err := Split(ctx, opts, t)
	if err != nil {
		return res, err
	}
	if commit == nil {
		return res, nil // dry run, or the file was refused with a verdict
	}
	if err := commit(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// Split runs everything except the final swap and hands the swap back as a
// function.
//
// The node uses this to start the next encode while the finished file is still
// being copied back to the library: that copy is thirty-odd seconds of I/O that
// has no business blocking the media engine. Anyone who does not call the
// returned function leaves the original untouched.
func Split(ctx context.Context, opts Options, t Task) (Result, func(context.Context) error, error) {
	cfg := opts.Config
	logf := opts.Log
	if logf == nil {
		logf = func(Event) {}
	}
	started := time.Now()
	var res Result

	emit := func(stage, code, msg string) {
		logf(Event{FileID: t.ID, Path: t.Path, Stage: stage, Code: code, Message: msg})
	}

	if _, err := os.Stat(t.Path); err != nil {
		return res, nil, abort(opts, t, "E_SOURCE_MISSING", err.Error())
	}
	before, err := Stat(t.Path)
	if err != nil {
		return res, nil, abort(opts, t, "E_SOURCE_MISSING", err.Error())
	}
	res.BytesBefore = before

	// One worker per file, enforced on disk as well as in the state machine.
	// Locks live in their own directory so they never look like scratch data.
	unlock, err := LockFile(filepath.Join(cfg.Paths.Scratch, "locks", fmt.Sprintf("%d.lock", t.ID)))
	if err != nil {
		return res, nil, abort(opts, t, "E_LOCKED", err.Error())
	}
	defer unlock()

	if err := ensureSpace(cfg, filepath.Dir(t.Path)); err != nil {
		return res, nil, abort(opts, t, "E_DISK_LOW", err.Error())
	}

	// Ingest: copy to fast storage first, and prove the copy is complete. This
	// also turns "the file is unreadable" into a cheap failure instead of an
	// expensive one twenty minutes into an encode.
	src, _, err := EnsureSource(ctx, cfg, t.ID, t.Path, func(stage, msg string) { emit(stage, "", msg) })
	if err != nil {
		var se *StageError
		if errors.As(err, &se) {
			return res, nil, abort(opts, t, se.Code, se.Message)
		}
		return res, nil, abort(opts, t, "E_SOURCE_MISSING", err.Error())
	}
	defer func() { _ = os.Remove(src) }()

	// Probe the copy, not the original: these are the facts the encoder acts on.
	probe, err := ffmpeg.Probe(ctx, src)
	if err != nil {
		return res, nil, abort(opts, t, "E_PROBE_FFPROBE", err.Error())
	}

	streams := planner.SelectStreams(probe, cfg, "")
	if len(streams.Audio) == 0 {
		// Refuse to produce a film with no audio. This is the safety valve that
		// keeps a language-policy bug from silently stripping every track.
		return res, nil, abort(opts, t, "E_NO_AUDIO_SELECTED",
			"audio policy selected no tracks; check audio.keep_languages")
	}

	spec := ffmpeg.EncodeSpec{
		Src:        src,
		VideoIndex: streams.VideoIndex,
		VideoCopy:  t.Decision == "remux",
	}
	for _, a := range streams.Audio {
		spec.Audio = append(spec.Audio, ffmpeg.AudioSpec{
			Index: a.Index, TranscodeTo: a.TranscodeTo, Kbps: a.Kbps,
		})
	}
	spec.Subs = streams.Subs

	out := OutputPath(cfg, t.ID)
	spec.Dst = out
	keepOutput := false
	defer func() {
		if !keepOutput {
			_ = os.Remove(out)
		}
	}()

	band := RatioBand{Min: cfg.Safety.SizeRatioMin, Max: cfg.Safety.SizeRatioMax}
	if t.Decision == "remux" {
		// A remux should be roughly the same size; the encode bands would
		// reject it for not shrinking.
		band = RatioBand{Min: 0.80, Max: 1.15}
	}

	// One encode at the ladder rung, plus one escalation if the result misses
	// the quality floor. Grain-heavy film is the usual cause; spending another
	// 40 % of bits beats parking a file that is otherwise fine.
	targets := []int{t.TargetKbps}
	if t.Decision == "encode" && t.TargetKbps > 0 {
		targets = append(targets, t.TargetKbps*14/10)
	}

	var verdict Verdict
	for attempt, kbps := range targets {
		spec.TargetKbps = kbps
		_ = os.Remove(out)

		if attempt > 0 {
			emit("retry", "E_VERIFY_VMAF", fmt.Sprintf("below the quality floor at %d kbps; retrying at %d kbps", targets[0], kbps))
		}
		_ = opts.Reporter.SetState(ctx, t.ID, "encoding")
		emit("encode", "", fmt.Sprintf("%s → %d kbps", t.Decision, kbps))

		var lastEmit time.Time
		encodeStarted := time.Now()
		// ffmpeg emits progress fields on separate lines, so each callback
		// carries only one of them. Accumulate or the percentage stays at zero.
		var prog struct {
			outUS int64
			speed float64
		}

		// The projected size is what a finished file should weigh at this rung.
		// Bytes on disk against it is the only progress measure that survives
		// files where out_time lags: a 35-minute episode with 40 subtitle
		// streams reports 3% while the output is two thirds written, and its
		// own speed field reads N/A throughout.
		expectedBytes := 0.0
		if t.Decision == "encode" && t.TargetKbps > 0 && probe.DurationS > 0 {
			expectedBytes = float64(t.TargetKbps) * 1000 / 8 * probe.DurationS
		}

		encodeTail, runErr := ffmpeg.RunTail(ctx, ffmpeg.Build(spec), func(p ffmpeg.Progress) {
			if p.OutTimeUS > 0 {
				prog.outUS = p.OutTimeUS
			}
			if p.Speed > 0 {
				prog.speed = p.Speed
			}
			if time.Since(lastEmit) < 2*time.Second {
				return
			}
			lastEmit = time.Now()

			fraction := 0.0
			if probe.DurationS > 0 && prog.outUS > 0 {
				fraction = float64(prog.outUS) / 1e6 / probe.DurationS
			}
			if expectedBytes > 0 {
				if fi, statErr := os.Stat(out); statErr == nil {
					if bySize := float64(fi.Size()) / expectedBytes; bySize > fraction {
						fraction = bySize
					}
				}
			}
			if fraction > 0.99 {
				fraction = 0.99 // never claim finished; verification has not run
			}

			// Report our own speed when ffmpeg's is missing or wrong: seconds of
			// film written per second of wall clock.
			speed := prog.speed
			if elapsed := time.Since(encodeStarted).Seconds(); elapsed > 0 && fraction > 0 {
				if measured := fraction * probe.DurationS / elapsed; measured > speed {
					speed = measured
				}
			}

			logf(Event{
				FileID: t.ID, Path: t.Path, Stage: "encode",
				Message:  fmt.Sprintf("%.0f%%", fraction*100),
				Fraction: fraction, Speed: speed,
			})
			// Also land it on the row, so the dashboard can read progress from
			// another process: the server never sees this callback.
			_ = opts.Reporter.SetProgress(ctx, t.ID, fraction, speed)
		})
		if runErr != nil {
			return res, nil, abort(opts, t, "E_ENCODE_FFMPEG", runErr.Error())
		}
		if info, statErr := os.Stat(out); statErr != nil || info.Size() == 0 {
			detail := "ffmpeg exited cleanly but wrote nothing"
			if tailText := strings.TrimSpace(encodeTail); tailText != "" {
				detail += ": " + tailText
			}
			return res, nil, abort(opts, t, "E_ENCODE_NO_OUTPUT", detail)
		}
		res.BytesAfter = mustSize(out)
		_ = opts.Reporter.SetProgress(ctx, t.ID, 1, prog.speed)

		// Verify before anything is allowed to move.
		_ = opts.Reporter.SetState(ctx, t.ID, "verifying")
		verdict, err = Verify(ctx, media.Probe{
			DurationS: probe.DurationS,
			SizeBytes: before,
		}, out, VerifyRules{
			Band:               band,
			DurationToleranceS: float64(cfg.Safety.DurationTolSec),
			SampleSeconds:      cfg.Safety.VerifySampleSec,
			VerifyVMAF:         cfg.Safety.VerifyVMAF && t.Decision == "encode",
			ExpectedVMAF:       planner.ExpectedVMAF(cfg, probe.VideoCodec, probe.Height),
			VMAFTolerance:      cfg.Quality.VMAFTolerance,
			RefPath:            t.Path,
		})
		if err != nil {
			return res, nil, abort(opts, t, "E_VERIFY_DECODE", err.Error())
		}
		if verdict.OK {
			break
		}
		// Only a quality miss is worth another encode. Anything else is a real
		// failure and retrying it unchanged would just burn the same time again.
		if verdict.Code != "E_VERIFY_VMAF" {
			res.Verdict = verdict
			return res, nil, abort(opts, t, verdict.Code, verdict.Detail)
		}
		if attempt == len(targets)-1 {
			// Even the escalated bitrate missed. That is a property of the
			// content (grain does not survive a re-encode), not a fault: leave
			// the file exactly as it is and say why.
			res.Verdict = verdict
			reason := fmt.Sprintf("best attempt scored %s at %d kbps: not worth re-encoding",
				verdictDetail(verdict), kbps)
			emit("skip", "R_QUALITY_FLOOR", reason)
			_ = opts.Reporter.Event(ctx, "info", "R_QUALITY_FLOOR", t.ID, reason, "")
			return res, nil, opts.Reporter.Skip(ctx, t.ID, "R_QUALITY_FLOOR", reason)
		}
	}
	res.Verdict = verdict
	if !verdict.OK {
		return res, nil, abort(opts, t, verdict.Code, verdict.Detail)
	}

	if opts.DryRun {
		emit("dry-run", "", fmt.Sprintf("verified %s → %s, not replacing", humanSize(before), humanSize(res.BytesAfter)))
		// Leave it claimable: a dry run must not consume the file.
		_ = opts.Reporter.SetState(ctx, t.ID, "planned")
		res.Seconds = int64(time.Since(started).Seconds())
		return res, nil, nil
	}

	// The swap belongs to the caller from here. keepOutput stops the deferred
	// cleanup: without it the output would be deleted before anyone could place
	// it, which is exactly the bug this flag exists to prevent.
	keepOutput = true
	res.Seconds = int64(time.Since(started).Seconds())
	commit := func(cctx context.Context) error {
		defer func() { _ = os.Remove(out) }()
		return Commit(cctx, opts, t, &res, before)
	}
	return res, commit, nil
}

// Commit places a verified output: copy next to the destination, quarantine the
// original, then swap atomically.
//
// It is separate from Split so a node can overlap it with the next encode. The
// safety property travels with it: the original is only touched by the final
// rename, and the quarantine step copies before anything moves.
func Commit(ctx context.Context, opts Options, t Task, res *Result, before int64) error {
	cfg := opts.Config
	logf := opts.Log
	if logf == nil {
		logf = func(Event) {}
	}
	emit := func(stage, code, msg string) {
		logf(Event{FileID: t.ID, Path: t.Path, Stage: stage, Code: code, Message: msg})
	}

	out := OutputPath(cfg, t.ID)
	_ = opts.Reporter.SetState(ctx, t.ID, "replacing")

	dir := filepath.Dir(t.Path)
	tmp := filepath.Join(dir, fmt.Sprintf(".messer-%d.tmp", t.ID))
	defer func() { _ = os.Remove(tmp) }()
	if err := CopyFile(out, tmp, nil); err != nil {
		return abort(opts, t, "E_REPLACE_COPY", err.Error())
	}

	q, err := Quarantine(t.Path, t.LibraryRoot, cfg.Paths.Quarantine, time.Now())
	if err != nil {
		return abort(opts, t, "E_QUARANTINE", err.Error())
	}
	res.Quarantine = q
	emit("replace", "", "original quarantined at "+q)

	if err := Replace(tmp, t.Path); err != nil {
		return abort(opts, t, "E_REPLACE_RENAME",
			fmt.Sprintf("%v (the original is safe at %s)", err, q))
	}

	if err := opts.Reporter.Finish(ctx, t.ID, before, res.BytesAfter, res.Verdict.VMAF); err != nil {
		return err
	}
	emit("done", "", fmt.Sprintf("%s → %s", humanSize(before), humanSize(res.BytesAfter)))
	return nil
}

// abort records the failure and leaves the file retryable until it runs out of
// attempts. It returns the error so the caller can decide whether to continue.
func abort(opts Options, t Task, code, msg string) error {
	max := 3
	if opts.Config != nil && opts.Config.Safety.MaxAttempts > 0 {
		max = opts.Config.Safety.MaxAttempts
	}
	state := "planned"
	if opts.Reporter != nil {
		if s, err := opts.Reporter.Fail(context.Background(), t.ID, code, msg, max); err == nil {
			state = s
		}
		_ = opts.Reporter.Event(context.Background(), "error", code, t.ID, msg, "")
	}
	return fmt.Errorf("%s: %s (state %s)", code, msg, state)
}

func ensureSpace(cfg *config.Config, destDir string) error {
	need := float64(cfg.Safety.MinFreeGB)
	for _, dir := range []string{cfg.Paths.Scratch, destDir} {
		free, err := freeGB(dir)
		if err != nil {
			return fmt.Errorf("cannot measure free space on %s: %w", dir, err)
		}
		if free < need {
			return fmt.Errorf("%s has %.0f GB free, %.0f GB required", dir, free, need)
		}
	}
	return nil
}

func freeGB(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return float64(st.Bavail) * float64(st.Bsize) / 1e9, nil
}

// verdictDetail pulls the human sentence out of a verdict.
func verdictDetail(v Verdict) string {
	if v.Detail != "" {
		return v.Detail
	}
	return v.Code
}

// mustSize returns a file's size, or zero when it is gone.
func mustSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// humanSize is decimal, matching how the library is described everywhere else.
func humanSize(bytes int64) string {
	switch {
	case bytes >= 1e12:
		return fmt.Sprintf("%.2f TB", float64(bytes)/1e12)
	case bytes >= 1e9:
		return fmt.Sprintf("%.2f GB", float64(bytes)/1e9)
	case bytes >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1e6)
	case bytes >= 1e3:
		return fmt.Sprintf("%.0f KB", float64(bytes)/1e3)
	}
	return fmt.Sprintf("%d B", bytes)
}
