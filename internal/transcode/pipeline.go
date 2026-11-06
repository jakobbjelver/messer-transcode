package transcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/ffmpeg"
	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/planner"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// Task is one file to process.
type Task struct {
	ID          int64
	Path        string
	LibraryRoot string
	Decision    string // encode | remux
	TargetKbps  int
}

// Options configure a run.
type Options struct {
	Config *config.Config
	Store  *store.Store
	Log    func(Event)
	DryRun bool // do everything except the final swap
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
		return res, abort(opts, t, "E_SOURCE_MISSING", err.Error())
	}
	before, err := Stat(t.Path)
	if err != nil {
		return res, abort(opts, t, "E_SOURCE_MISSING", err.Error())
	}
	res.BytesBefore = before

	// One worker per file, enforced on disk as well as in the state machine.
	// Locks live in their own directory so they never look like scratch data.
	unlock, err := LockFile(filepath.Join(cfg.Paths.Scratch, "locks", fmt.Sprintf("%d.lock", t.ID)))
	if err != nil {
		return res, abort(opts, t, "E_LOCKED", err.Error())
	}
	defer unlock()

	if err := ensureSpace(cfg, filepath.Dir(t.Path)); err != nil {
		return res, abort(opts, t, "E_DISK_LOW", err.Error())
	}

	// Ingest: copy to fast storage first, and prove the copy is complete. This
	// also turns "the file is unreadable" into a cheap failure instead of an
	// expensive one twenty minutes into an encode.
	src := filepath.Join(cfg.Paths.Scratch, fmt.Sprintf("%d.src.mkv", t.ID))
	emit("ingest", "", "copying to scratch")
	if err := CopyFile(t.Path, src, nil); err != nil {
		return res, abort(opts, t, "E_SOURCE_MISSING", "ingest: "+err.Error())
	}
	defer os.Remove(src)

	same, err := SameSize(t.Path, src)
	if err != nil || !same {
		return res, abort(opts, t, "E_SOURCE_CHANGED", "ingested copy does not match the source size")
	}

	// Probe the copy, not the original: these are the facts the encoder acts on.
	probe, err := ffmpeg.Probe(ctx, src)
	if err != nil {
		return res, abort(opts, t, "E_PROBE_FFPROBE", err.Error())
	}

	streams := planner.SelectStreams(probe, cfg, "")
	if len(streams.Audio) == 0 {
		// Refuse to produce a film with no audio. This is the safety valve that
		// keeps a language-policy bug from silently stripping every track.
		return res, abort(opts, t, "E_NO_AUDIO_SELECTED",
			"audio policy selected no tracks; check audio.keep_languages")
	}

	spec := ffmpeg.EncodeSpec{
		Src:        src,
		VideoIndex: streams.VideoIndex,
		VideoCopy:  t.Decision == "remux",
		TargetKbps: t.TargetKbps,
	}
	for _, a := range streams.Audio {
		spec.Audio = append(spec.Audio, ffmpeg.AudioSpec{
			Index: a.Index, TranscodeTo: a.TranscodeTo, Kbps: a.Kbps,
		})
	}
	spec.Subs = streams.Subs

	out := filepath.Join(cfg.Paths.Scratch, fmt.Sprintf("%d.out.mkv", t.ID))
	spec.Dst = out
	defer os.Remove(out)

	opts.Store.SetState(ctx, t.ID, "encoding")
	emit("encode", "", fmt.Sprintf("%s → %d kbps", t.Decision, t.TargetKbps))

	var lastEmit time.Time
	err = ffmpeg.Run(ctx, ffmpeg.Build(spec), func(p ffmpeg.Progress) {
		// ffmpeg emits progress fields separately; keep the last known value of
		// each and only report a few times a second.
		if time.Since(lastEmit) < 2*time.Second {
			return
		}
		lastEmit = time.Now()
		fraction := 0.0
		if probe.DurationS > 0 && p.OutTimeUS > 0 {
			fraction = float64(p.OutTimeUS) / 1e6 / probe.DurationS
			if fraction > 1 {
				fraction = 1
			}
		}
		logf(Event{
			FileID: t.ID, Path: t.Path, Stage: "encode",
			Message:  fmt.Sprintf("%.0f%%", fraction*100),
			Fraction: fraction, Speed: p.Speed,
		})
	})
	if err != nil {
		return res, abort(opts, t, "E_ENCODE_FFMPEG", err.Error())
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		return res, abort(opts, t, "E_ENCODE_NO_OUTPUT", "ffmpeg exited cleanly but wrote nothing")
	}
	res.BytesAfter = mustSize(out)

	// Verify before anything is allowed to move.
	opts.Store.SetState(ctx, t.ID, "verifying")
	band := RatioBand{Min: cfg.Safety.SizeRatioMin, Max: cfg.Safety.SizeRatioMax}
	if t.Decision == "remux" {
		// A remux should be roughly the same size; the encode bands would
		// reject it for not shrinking.
		band = RatioBand{Min: 0.80, Max: 1.15}
	}
	verdict, err := Verify(ctx, media.Probe{
		DurationS: probe.DurationS,
		SizeBytes: before,
	}, out, VerifyRules{
		Band:               band,
		DurationToleranceS: float64(cfg.Safety.DurationTolSec),
		SampleSeconds:      cfg.Safety.VerifySampleSec,
	})
	if err != nil {
		return res, abort(opts, t, "E_VERIFY_DECODE", err.Error())
	}
	res.Verdict = verdict
	if !verdict.OK {
		return res, abort(opts, t, verdict.Code, verdict.Detail)
	}

	if opts.DryRun {
		emit("dry-run", "", fmt.Sprintf("verified %s → %s, not replacing", humanSize(before), humanSize(res.BytesAfter)))
		// Leave it claimable: a dry run must not consume the file.
		_ = opts.Store.SetState(ctx, t.ID, "planned")
		res.Seconds = int64(time.Since(started).Seconds())
		return res, nil
	}

	// Place: copy next to the destination, then swap atomically.
	opts.Store.SetState(ctx, t.ID, "replacing")
	dir := filepath.Dir(t.Path)
	tmp := filepath.Join(dir, fmt.Sprintf(".messer-%d.tmp", t.ID))
	defer os.Remove(tmp)
	if err := CopyFile(out, tmp, nil); err != nil {
		return res, abort(opts, t, "E_REPLACE_COPY", err.Error())
	}

	q, err := Quarantine(t.Path, t.LibraryRoot, cfg.Paths.Quarantine, time.Now())
	if err != nil {
		return res, abort(opts, t, "E_QUARANTINE", err.Error())
	}
	res.Quarantine = q
	emit("replace", "", "original quarantined at "+q)

	if err := Replace(tmp, t.Path); err != nil {
		return res, abort(opts, t, "E_REPLACE_RENAME",
			fmt.Sprintf("%v (the original is safe at %s)", err, q))
	}

	if err := opts.Store.Finish(ctx, t.ID, before, res.BytesAfter, 0); err != nil {
		return res, err
	}
	res.Seconds = int64(time.Since(started).Seconds())
	emit("done", "", fmt.Sprintf("%s → %s", humanSize(before), humanSize(res.BytesAfter)))
	return res, nil
}

// abort records the failure and leaves the file retryable until it runs out of
// attempts. It returns the error so the caller can decide whether to continue.
func abort(opts Options, t Task, code, msg string) error {
	max := 3
	if opts.Config != nil && opts.Config.Safety.MaxAttempts > 0 {
		max = opts.Config.Safety.MaxAttempts
	}
	state := "planned"
	if opts.Store != nil {
		if s, err := opts.Store.Fail(context.Background(), t.ID, code, msg, max); err == nil {
			state = s
		}
		_ = opts.Store.Event(context.Background(), "error", code, t.ID, msg, "")
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
