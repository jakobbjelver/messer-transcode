// Package transcode performs the write path: encode, verify, replace. Every
// step is reversible until the last rename, and nothing in here deletes a file.
package transcode

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/jakobbjelver/messer-transcode/internal/ffmpeg"
	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// Check is one verification step and its outcome.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Verdict is the result of verifying an output file.
type Verdict struct {
	OK     bool        `json:"ok"`
	Code   string      `json:"code,omitempty"`
	Detail string      `json:"detail,omitempty"`
	VMAF   float64     `json:"vmaf,omitempty"`
	Probe  media.Probe `json:"-"`
	Checks []Check     `json:"checks"`
}

// RatioBand bounds the output/source size ratio.
type RatioBand struct {
	Min, Max float64
}

// VerifyRules is the slice of configuration verification needs.
type VerifyRules struct {
	Band               RatioBand
	DurationToleranceS float64
	SampleSeconds      int
	// VerifyVMAF turns on the quality check. It only runs when RefPath is set
	// and ExpectedVMAF is above zero — a rung with no measurement behind it
	// cannot be held to a number.
	VerifyVMAF    bool
	ExpectedVMAF  float64
	VMAFTolerance float64
	RefPath       string
}

// Verify checks an encoded file before it is allowed to replace anything:
// it decodes, it is the right length, and it is a plausible size.
func Verify(ctx context.Context, src media.Probe, out string, rules VerifyRules) (Verdict, error) {
	v := Verdict{OK: true}

	got, err := ffmpeg.Probe(ctx, out)
	if err != nil {
		return Verdict{OK: false, Code: "E_VERIFY_PROBE", Detail: err.Error()}, nil
	}
	v.Probe = got

	// 1. Duration.
	delta := got.DurationS - src.DurationS
	tol := rules.DurationToleranceS
	if tol < 0 {
		tol = 2
	}
	ok := delta <= tol && delta >= -tol
	v.Checks = append(v.Checks, Check{
		Name: "duration", OK: ok,
		Code:   "E_VERIFY_DURATION",
		Detail: fmt.Sprintf("%.1fs vs %.1fs (Δ %.1fs, tolerance %.0fs)", got.DurationS, src.DurationS, delta, tol),
	})

	// 2. Size ratio, against the source as it exists on disk.
	ratio := 0.0
	if src.SizeBytes > 0 {
		ratio = float64(got.SizeBytes) / float64(src.SizeBytes)
	}
	ok = ratio >= rules.Band.Min && ratio <= rules.Band.Max
	v.Checks = append(v.Checks, Check{
		Name: "size_ratio", OK: ok,
		Code:   "E_VERIFY_SIZE_RATIO",
		Detail: fmt.Sprintf("%.3f (allowed %.2f–%.2f)", ratio, rules.Band.Min, rules.Band.Max),
	})

	// 3. Sampled decode: head, tail and two random windows. A full decode of a
	// two-hour film would cost more than the encode did, and every real failure
	// mode (truncation, a broken final GOP, a bad seek index) shows up in one of
	// these four places.
	decodeErr := verifyDecode(ctx, out, got.DurationS, rules.SampleSeconds)
	v.Checks = append(v.Checks, Check{
		Name: "decode", OK: decodeErr == nil,
		Code:   "E_VERIFY_DECODE",
		Detail: errorText(decodeErr),
	})

	// 4. Quality, when there is a measured expectation to hold it to.
	// Two windows, averaged: a single 60-second sample varies by several points
	// on grain-heavy content, and holding it to a mean measured over many
	// windows would fail films that are perfectly fine.
	if rules.VerifyVMAF && rules.RefPath != "" && rules.ExpectedVMAF > 0 {
		window := float64(rules.SampleSeconds)
		starts := make([]float64, 0, 2)
		for _, frac := range []float64{0.40, 0.70} {
			start := got.DurationS * frac
			if start+window > got.DurationS {
				start = got.DurationS - window
			}
			if start < 0 {
				start = 0
			}
			starts = append(starts, start)
		}

		// The windows are independent measurements of the same encode, so they
		// run together. Sequentially this was eleven seconds of the media engine
		// sitting idle between files, which is most of the idle time the lanes
		// are meant to remove.
		type sample struct {
			score ffmpeg.VMAFResult
			err   error
		}
		results := make([]sample, len(starts))
		var wg sync.WaitGroup
		for i, start := range starts {
			wg.Add(1)
			go func(i int, start float64) {
				defer wg.Done()
				score, err := ffmpeg.VMAFWindow(ctx, rules.RefPath, start, out, start, window)
				results[i] = sample{score: score, err: err}
			}(i, start)
		}
		wg.Wait()

		var scores []float64
		var worstMin float64 = 101
		var measureErr error
		for _, r := range results {
			if r.err != nil {
				measureErr = r.err
				break
			}
			scores = append(scores, r.score.Mean)
			if r.score.Min < worstMin {
				worstMin = r.score.Min
			}
		}
		switch {
		case measureErr != nil:
			v.Checks = append(v.Checks, Check{
				Name: "vmaf", OK: true, // a measurement failure is not a quality failure
				Code:   "E_VERIFY_VMAF",
				Detail: "not measured: " + measureErr.Error(),
			})
		case len(scores) > 0:
			mean := 0.0
			for _, s := range scores {
				mean += s
			}
			mean /= float64(len(scores))
			v.VMAF = mean
			floor := rules.ExpectedVMAF - rules.VMAFTolerance
			ok := mean >= floor
			v.Checks = append(v.Checks, Check{
				Name: "vmaf", OK: ok,
				Code: "E_VERIFY_VMAF",
				Detail: fmt.Sprintf("%.1f over %d windows (expected %.1f, floor %.1f, worst frame %.1f)",
					mean, len(scores), rules.ExpectedVMAF, floor, worstMin),
			})
		}
	}

	for _, c := range v.Checks {
		if !c.OK {
			v.OK = false
			if v.Code == "" {
				v.Code = c.Code
				v.Detail = fmt.Sprintf("%s: %s", c.Name, c.Detail)
			}
		}
	}
	return v, nil
}

// muxerDTSNoise is the one stderr line ffmpeg writes on a healthy file.
//
// When a source carries PTS equal to DTS, the null muxer refuses a timestamp
// that does not increase and says so once per packet — while the decode
// succeeds and ffmpeg exits 0. Treating "stderr is not empty" as "the decode
// failed" parked ten healthy titles after three encodes each (2026-11-08..10).
const muxerDTSNoise = "non monotonically increasing dts to muxer"

// decodeVerdict turns one ffmpeg run into pass or fail.
//
// The exit code is the verdict. Stderr is evidence: the known timestamp noise
// is dropped, and any other error line — even at exit 0 — is still a failure,
// because we have no evidence about what else can appear there.
func decodeVerdict(runErr error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	var real []string
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, muxerDTSNoise) {
			continue
		}
		real = append(real, line)
	}
	if runErr != nil {
		if len(real) > 0 {
			return fmt.Errorf("%v: %s", runErr, strings.Join(real, " | "))
		}
		return runErr
	}
	if len(real) > 0 {
		return errors.New(strings.Join(real, " | "))
	}
	return nil
}

// verifyDecode decodes a few short windows to /dev/null.
func verifyDecode(ctx context.Context, path string, duration float64, window int) error {
	if duration <= 0 {
		return fmt.Errorf("unknown duration")
	}
	w := float64(window)
	starts := []float64{0}
	if duration > w*2 {
		starts = append(starts, duration-w)
		r := rand.New(rand.NewSource(int64(len(path))))
		for i := 0; i < 2; i++ {
			max := duration - w
			if max <= 0 {
				break
			}
			starts = append(starts, r.Float64()*max)
		}
	}

	for _, start := range starts {
		args := []string{
			"-hide_banner", "-nostdin", "-loglevel", "error",
			"-ss", fmt.Sprintf("%.3f", start),
			"-t", fmt.Sprintf("%.0f", w),
			"-i", path,
			"-f", "null", "-",
		}
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if v := decodeVerdict(cmd.Run(), stderr.String()); v != nil {
			return fmt.Errorf("window at %.0fs: %w", start, v)
		}
	}
	return nil
}

// DecodeFully is the opt-in exhaustive check, used by the sweep job.
func DecodeFully(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", path, "-f", "null", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	return decodeVerdict(cmd.Run(), stderr.String())
}

func errorText(err error) string {
	if err == nil {
		return "four sampled windows decoded cleanly"
	}
	return err.Error()
}

// Stat is a small helper so callers do not import os just for a size.
func Stat(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
