// Package plan orchestrates the read-only path: scan, probe, decide, summarise.
// It writes to the state database and nothing else — no media file is created,
// moved or deleted anywhere in this package.
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/ffmpeg"
	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/node"
	"github.com/jakobbjelver/messer-transcode/internal/planner"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// Options configures a plan run.
type Options struct {
	Config    *config.Config
	Store     *store.Store
	Workers   int
	Limit     int  // probe at most N files; 0 means all
	SkipProbe bool // decide from existing probes only
	// Redecide re-runs the planner over rows that are queued or were refused,
	// after the normal pass. It is how a ladder change reaches verdicts taken
	// before it; finished, retired and in-flight rows are excluded.
	Redecide    bool
	RedecideIDs []int64 // narrow the re-decision to these rows; nil means all
	Progress    func(Progress)
}

// Progress is one line of feed while the run works.
type Progress struct {
	Phase string `json:"phase"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Path  string `json:"path,omitempty"`
	ETA   string `json:"eta,omitempty"`
}

// Report is the summary a caller prints.
type Report struct {
	Scanned      int            `json:"scanned"`
	LibraryStats []LibStat      `json:"library_stats"`
	NeedingProbe int            `json:"needing_probe"`
	Probed       int            `json:"probed"`
	ProbeErrors  int            `json:"probe_errors"`
	Decided      int            `json:"decided"`
	Redecided    int            `json:"redecided"`
	Gone         int            `json:"gone"`
	Warnings     []string       `json:"warnings,omitempty"`
	Buckets      []store.Bucket `json:"buckets"`
	States       map[string]int `json:"states"`
	TotalBytes   int64          `json:"total_bytes"`
	Projected    int64          `json:"projected_bytes"`
	Reclaimable  int64          `json:"reclaimable_bytes"`
	Elapsed      string         `json:"elapsed"`
}

// LibStat is per-library scan detail.
type LibStat struct {
	Name     string `json:"name"`
	Files    int    `json:"files"`
	Skipped  int    `json:"skipped"`
	Filtered int    `json:"filtered"`
}

// Run executes the plan.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Progress == nil {
		opts.Progress = func(Progress) {}
	}
	started := time.Now()
	rep := &Report{}

	// 1. Scan, then retire whatever the walk did not find.
	//
	// The stamp is taken before the walk and every file in the set carries it,
	// so "not seen" is a comparison between two numbers instead of a second
	// clock read that can race the scan.
	for _, lib := range opts.Config.Libraries {
		stamp := time.Now().Unix()
		res, err := node.Scan(lib)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", lib.Name, err)
		}
		if _, _, err := opts.Store.UpsertScannedAt(ctx, lib.Name, res.Files, stamp); err != nil {
			return nil, fmt.Errorf("record %s: %w", lib.Name, err)
		}
		rep.Scanned += len(res.Files)
		rep.LibraryStats = append(rep.LibraryStats, LibStat{
			Name:     lib.Name,
			Files:    len(res.Files),
			Skipped:  res.Skipped,
			Filtered: res.Filtered,
		})

		// An unmounted volume walks as an empty library, and a prune taken from
		// that silence would retire the whole catalogue. The root has to exist
		// for the walk's answer to mean anything.
		if !rootExists(lib.Root) {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf(
				"%s: root %s is not a directory — nothing retired from it", lib.Name, lib.Root))
			continue
		}
		gone, err := opts.Store.MarkGone(ctx, lib.Name, stamp)
		if err != nil {
			return nil, fmt.Errorf("retire %s: %w", lib.Name, err)
		}
		rep.Gone += gone
	}

	// 2. Probe.
	pending, err := opts.Store.FilesNeedingProbe(ctx)
	if err != nil {
		return nil, err
	}
	rep.NeedingProbe = len(pending)

	if !opts.SkipProbe && len(pending) > 0 {
		queue := pending
		if opts.Limit > 0 && opts.Limit < len(queue) {
			queue = queue[:opts.Limit]
		}
		probed, failed := probeAll(ctx, opts, queue)
		rep.Probed = probed
		rep.ProbeErrors = failed
	}

	// 3. Decide.
	undecided, err := opts.Store.FilesNeedingDecision(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range undecided {
		v := planner.DecideFile(u.Path, opts.Config, plannerFacts(u))
		d := store.Decision{
			Decision: v.Decision,
			Code:     v.Code,
			Reason:   v.Reason,
			KeyKbps:  v.TargetKbps,
			State:    v.State(),
		}
		if err := opts.Store.SetDecision(ctx, u.ID, d); err != nil {
			return nil, fmt.Errorf("decide %s: %w", u.Path, err)
		}
		rep.Decided++
	}

	// 3b. Re-decide on request. The planner only judges rows with no verdict,
	// so a rung that moved leaves every settled verdict priced at the old one;
	// this is the explicit way to apply a ladder change to the queue.
	if opts.Redecide {
		n, err := Redecide(ctx, opts.Store, opts.Config, opts.RedecideIDs)
		if err != nil {
			return nil, err
		}
		rep.Redecided = n
	}

	// 4. Summarise.
	rep.Buckets, err = opts.Store.Summary(ctx)
	if err != nil {
		return nil, err
	}
	rep.States, err = opts.Store.StateCounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range rep.Buckets {
		rep.TotalBytes += b.SizeBytes
		rep.Projected += b.EstimateOut
	}
	if rep.TotalBytes > rep.Projected {
		rep.Reclaimable = rep.TotalBytes - rep.Projected
	}
	rep.Elapsed = time.Since(started).Round(time.Second).String()
	return rep, nil
}

// rootExists is the guard that makes a prune safe: an unmounted volume and an
// empty library look identical to a walk, and only one of them may retire rows.
func rootExists(root string) bool {
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// Redecide re-runs the planner over queued and refused rows and returns how
// many verdicts it wrote. ids narrows it to those rows; nil means every row the
// store considers re-decidable.
//
// It is exported because it is the operation an operator reaches for after a
// ladder change, and because the alternative — a plan run that silently leaves
// every previous verdict in place — is how a tier fix looks applied and isn't.
func Redecide(ctx context.Context, st *store.Store, cfg *config.Config, ids []int64) (int, error) {
	rows, err := st.Redecidable(ctx, ids)
	if err != nil {
		return 0, err
	}
	for _, u := range rows {
		v := planner.DecideFile(u.Path, cfg, plannerFacts(u))
		d := store.Decision{
			Decision: v.Decision,
			Code:     v.Code,
			Reason:   v.Reason,
			KeyKbps:  v.TargetKbps,
			State:    v.State(),
		}
		if err := st.SetDecision(ctx, u.ID, d); err != nil {
			return 0, fmt.Errorf("re-decide %s: %w", u.Path, err)
		}
	}
	return len(rows), nil
}

func plannerFacts(u store.Undecided) media.Facts {
	f := media.Facts{Width: u.Width, Height: u.Height, Codec: u.Codec, BitrateKbps: u.BitrateKbps}

	// The rule list can read audio, so the facts must carry it. A file whose
	// audio JSON is missing or unreadable projects to empty audio facts and
	// simply never matches an audio rule.
	var tracks []media.AudioTrack
	if u.AudioTracks != "" && json.Unmarshal([]byte(u.AudioTracks), &tracks) == nil {
		f.AudioTracks = len(tracks)
		for _, t := range tracks {
			f.AudioCodecs = append(f.AudioCodecs, strings.ToLower(t.Codec))
		}
	}
	return f
}

func probeAll(ctx context.Context, opts Options, queue []store.Pending) (probed, failed int) {
	work := make(chan store.Pending)
	var done, okCount, failCount atomic.Int64
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				if ctx.Err() != nil {
					return
				}
				pr, err := ffmpeg.Probe(ctx, p.Path)
				if err != nil {
					failCount.Add(1)
					_ = opts.Store.SetProbeError(ctx, p.ID, ffmpeg.ErrFFProbe, err.Error())
				} else {
					okCount.Add(1)
					tmdbID := planner.ParseTMDBID(p.Path)
					_ = opts.Store.SetProbe(ctx, p.ID, pr, store.ProbeKey(p.Size, p.Mtime), tmdbID)
				}
				n := int(done.Add(1))
				if n%25 == 0 || n == len(queue) {
					opts.Progress(Progress{
						Phase: "probe",
						Done:  n,
						Total: len(queue),
						Path:  p.Path,
						ETA:   eta(start, n, len(queue)),
					})
				}
			}
		}()
	}

	for _, p := range queue {
		select {
		case work <- p:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return int(okCount.Load()), int(failCount.Load())
		}
	}
	close(work)
	wg.Wait()
	return int(okCount.Load()), int(failCount.Load())
}

func eta(start time.Time, done, total int) string {
	if done == 0 {
		return ""
	}
	per := time.Since(start) / time.Duration(done)
	left := time.Duration(total-done) * per
	return left.Round(time.Second).String()
}
