// Package calibrate answers one question with measurements instead of guesses:
// how many bits does this encoder need to hit a quality target on this library?
//
// It encodes short windows at several bitrates, scores each against the source
// with VMAF, and reports the lowest rung that meets the target per codec tier.
package calibrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/ffmpeg"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// Options configures a calibration run.
type Options struct {
	Config         *config.Config
	Store          *store.Store
	Target         float64
	PerTier        int
	Rungs          []int
	WindowSeconds  float64
	WindowsPerFile int
	Progress       func(Event)
}

// Event is one line of progress.
type Event struct {
	Stage string  `json:"stage"`
	File  string  `json:"file,omitempty"`
	Codec string  `json:"codec,omitempty"`
	Kbps  int     `json:"kbps,omitempty"`
	VMAF  float64 `json:"vmaf,omitempty"`
	Done  int     `json:"done"`
	Total int     `json:"total"`
}

// Sample is one file chosen to represent its tier.
type Sample struct {
	Path        string  `json:"path"`
	Codec       string  `json:"codec"`
	Height      int     `json:"height"`
	BitrateKbps int     `json:"bitrate_kbps"`
	DurationS   float64 `json:"duration_s"`
}

// Point is one measurement: a bitrate and the VMAF it produced.
type Point struct {
	Kbps   int     `json:"kbps"`
	VMAF   float64 `json:"vmaf"`
	Sample int     `json:"sample"`
}

// Curve is the measured quality/bitrate relationship for one codec tier.
type Curve struct {
	Codec  string `json:"codec"`
	Height int    `json:"height"`
	Rungs  []Rung `json:"rungs"`
	Chosen int    `json:"chosen_kbps"`
	Met    bool   `json:"target_met"`
}

// Rung is the aggregate for one bitrate across all windows of all samples.
type Rung struct {
	Kbps     int     `json:"kbps"`
	VMAF     float64 `json:"vmaf"`
	MinVMAF  float64 `json:"min_vmaf"`
	Measures int     `json:"measures"`
}

// SuggestRow is one proposed ladder entry. Extrapolated marks rungs that were
// scaled from the calibrated 1080p figure rather than measured.
type SuggestRow struct {
	Codec        string `json:"codec"`
	HeightGTE    int    `json:"height_gte"`
	Kbps         int    `json:"kbps"`
	Extrapolated bool   `json:"extrapolated,omitempty"`
}

// Result is the whole run.
type Result struct {
	Target    float64      `json:"target"`
	Samples   []Sample     `json:"samples"`
	Curves    []Curve      `json:"curves"`
	Suggested []SuggestRow `json:"suggested_table"`
	Elapsed   string       `json:"elapsed"`
}

// Run performs the calibration.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Target <= 0 {
		opts.Target = opts.Config.Quality.VMAFTarget
	}
	if opts.PerTier <= 0 {
		opts.PerTier = 3
	}
	if len(opts.Rungs) == 0 {
		opts.Rungs = []int{4000, 5000, 6000, 7000, 8500}
	}
	if opts.WindowSeconds <= 0 {
		opts.WindowSeconds = 60
	}
	if opts.WindowsPerFile <= 0 {
		opts.WindowsPerFile = 2
	}
	if opts.Progress == nil {
		opts.Progress = func(Event) {}
	}
	sort.Ints(opts.Rungs)

	started := time.Now()
	res := &Result{Target: opts.Target}

	samples, err := pickSamples(ctx, opts)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("no encode candidates found to calibrate against")
	}
	res.Samples = samples

	// One curve per codec, at the 1080p rung.
	byCodec := map[string][]Point{}
	total := len(samples) * len(opts.Rungs) * opts.WindowsPerFile
	done := 0

	for i, s := range samples {
		for _, kbps := range opts.Rungs {
			for w := 0; w < opts.WindowsPerFile; w++ {
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				start := windowStart(s.DurationS, opts.WindowSeconds, w, opts.WindowsPerFile)
				score, err := measure(ctx, opts, s, start, kbps, i)
				done++
				if err != nil {
					opts.Progress(Event{Stage: "skip", File: filepath.Base(s.Path), Codec: s.Codec, Kbps: kbps, Done: done, Total: total})
					continue
				}
				byCodec[s.Codec] = append(byCodec[s.Codec], Point{Kbps: kbps, VMAF: score, Sample: i})
				opts.Progress(Event{
					Stage: "measure", File: filepath.Base(s.Path), Codec: s.Codec,
					Kbps: kbps, VMAF: score, Done: done, Total: total,
				})
			}
		}
	}

	for _, codec := range []string{"h264", "hevc"} {
		points, ok := byCodec[codec]
		if !ok {
			continue
		}
		curve := buildCurve(codec, 1080, points, opts.Target)
		res.Curves = append(res.Curves, curve)
		if curve.Met {
			res.Suggested = append(res.Suggested, SuggestRow{
				Codec: codec, HeightGTE: 1000, Kbps: curve.Chosen,
			})
		}
	}
	// Higher and lower rungs are scaled from the calibrated 1080p figure: 4K
	// needs roughly 2.6x, 720p about 0.45x. These are extrapolations and are
	// labelled as such in the output.
	for _, c := range res.Curves {
		if !c.Met {
			continue
		}
		res.Suggested = append(res.Suggested,
			SuggestRow{Codec: c.Codec, HeightGTE: 2000, Kbps: c.Chosen * 26 / 10, Extrapolated: true},
			SuggestRow{Codec: c.Codec, HeightGTE: 700, Kbps: c.Chosen * 45 / 100, Extrapolated: true},
			SuggestRow{Codec: c.Codec, HeightGTE: 0, Kbps: c.Chosen * 16 / 100, Extrapolated: true},
		)
	}
	sortRows(res.Suggested)

	res.Elapsed = time.Since(started).Round(time.Second).String()
	logRun(opts, res)
	return res, nil
}

func measure(ctx context.Context, opts Options, s Sample, start float64, kbps int, index int) (float64, error) {
	out := filepath.Join(os.TempDir(), fmt.Sprintf("cal-%d-%d.mkv", index, kbps))
	defer func() { _ = os.Remove(out) }()

	if err := ffmpeg.Run(ctx, ffmpeg.BuildSampleEncode(s.Path, out, start, opts.WindowSeconds, kbps), nil); err != nil {
		return 0, err
	}
	score, err := ffmpeg.VMAFWindow(ctx, s.Path, start, out, 0, opts.WindowSeconds)
	if err != nil {
		return 0, err
	}
	return score.Mean, nil
}

// windowStart spreads windows through the film rather than clustering them at
// the head, where titles and logos compress unlike the actual feature.
func windowStart(duration, window float64, index, count int) float64 {
	if duration <= window*2 {
		return 0
	}
	usable := duration - window
	step := usable / float64(count+1)
	return step * float64(index+1)
}

func buildCurve(codec string, height int, points []Point, target float64) Curve {
	byKbps := map[int][]float64{}
	for _, p := range points {
		byKbps[p.Kbps] = append(byKbps[p.Kbps], p.VMAF)
	}
	var rungs []Rung
	for kbps, scores := range byKbps {
		sum, min := 0.0, 101.0
		for _, s := range scores {
			sum += s
			if s < min {
				min = s
			}
		}
		rungs = append(rungs, Rung{
			Kbps: kbps, VMAF: sum / float64(len(scores)), MinVMAF: min, Measures: len(scores),
		})
	}
	sort.Slice(rungs, func(i, j int) bool { return rungs[i].Kbps < rungs[j].Kbps })

	curve := Curve{Codec: codec, Height: height, Rungs: rungs}
	for _, r := range rungs {
		if r.VMAF >= target {
			curve.Chosen = r.Kbps
			curve.Met = true
			break
		}
	}
	return curve
}

// pickSamples takes encode candidates per codec, spread by size so the sample
// is not all blockbusters or all episodes.
func pickSamples(ctx context.Context, opts Options) ([]Sample, error) {
	rows, err := opts.Store.DB().QueryContext(ctx, `
SELECT path, video_codec, COALESCE(height,0), COALESCE(bitrate_kbps,0), COALESCE(duration_s,0)
FROM files
WHERE decision = 'encode' AND duration_s > 600 AND height >= 1000
ORDER BY video_codec, path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byCodec := map[string][]Sample{}
	for rows.Next() {
		var s Sample
		if err := rows.Scan(&s.Path, &s.Codec, &s.Height, &s.BitrateKbps, &s.DurationS); err != nil {
			return nil, err
		}
		byCodec[s.Codec] = append(byCodec[s.Codec], s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Sample
	for _, codec := range []string{"h264", "hevc"} {
		candidates := byCodec[codec]
		if len(candidates) == 0 {
			continue
		}
		per := opts.PerTier
		if len(candidates) < per {
			per = len(candidates)
		}
		// Even spacing through the sorted list gives a spread of titles, sizes
		// and bitrates without randomness that changes between runs.
		step := len(candidates) / per
		if step < 1 {
			step = 1
		}
		taken := 0
		for i := 0; i < len(candidates) && taken < per; i += step {
			out = append(out, candidates[i])
			taken++
		}
	}
	return out, nil
}

func sortRows(rows []SuggestRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Codec != rows[j].Codec {
			return rows[i].Codec < rows[j].Codec
		}
		return rows[i].HeightGTE > rows[j].HeightGTE
	})
}

func logRun(opts Options, res *Result) {
	if opts.Store == nil {
		return
	}
	data, _ := json.Marshal(res)
	_ = opts.Store.Event(context.Background(), "info", "CALIBRATION", 0,
		fmt.Sprintf("calibrated %d samples against VMAF %.1f", len(res.Samples), res.Target), string(data))
}
