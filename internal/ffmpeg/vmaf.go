package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// VMAFResult is a quality score against a reference.
type VMAFResult struct {
	Mean     float64 `json:"mean"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Harmonic float64 `json:"harmonic"`
	Frames   int     `json:"frames"`
}

// VMAF compares the same time window of two files.
func VMAF(ctx context.Context, ref, dist string, startSec, durSec float64) (VMAFResult, error) {
	return VMAFWindow(ctx, ref, startSec, dist, startSec, durSec)
}

// VMAFWindow compares two windows that may start at different offsets, which is
// what a sample encode produces: the output's window begins at zero.
func VMAFWindow(ctx context.Context, ref string, refStart float64, dist string, distStart float64, durSec float64) (VMAFResult, error) {
	logFile, err := os.CreateTemp("", "vmaf-*.json")
	if err != nil {
		return VMAFResult{}, err
	}
	logPath := logFile.Name()
	_ = logFile.Close()
	defer func() { _ = os.Remove(logPath) }()

	filter := fmt.Sprintf(
		"[0:v]setpts=PTS-STARTPTS[r];[1:v]setpts=PTS-STARTPTS[d];[d][r]libvmaf=n_threads=4:log_fmt=json:log_path=%s",
		logPath,
	)
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-ss", strconv.FormatFloat(refStart, 'f', 3, 64),
		"-t", strconv.FormatFloat(durSec, 'f', 3, 64),
		"-i", ref,
		"-ss", strconv.FormatFloat(distStart, 'f', 3, 64),
		"-t", strconv.FormatFloat(durSec, 'f', 3, 64),
		"-i", dist,
		"-lavfi", filter,
		"-f", "null", "-",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return VMAFResult{}, fmt.Errorf("vmaf: %s", strings.TrimSpace(stderr.String()))
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		return VMAFResult{}, fmt.Errorf("vmaf produced no log: %w", err)
	}
	return parseVMAF(data)
}

// BuildSampleEncode encodes a short window of the video only, at a fixed
// bitrate, using the same encoder settings as a real job. Calibration is only
// meaningful if the sample is produced by the same command.
func BuildSampleEncode(src, dst string, startSec, durSec float64, kbps int) []string {
	maxrate := kbps * 3 / 2
	bufsize := maxrate * 2
	return []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-nostats",
		"-ss", strconv.FormatFloat(startSec, 'f', 3, 64),
		"-t", strconv.FormatFloat(durSec, 'f', 3, 64),
		"-i", src,
		"-map", "0:v:0",
		"-c:v", "hevc_videotoolbox",
		"-b:v", fmt.Sprintf("%dk", kbps),
		"-maxrate", fmt.Sprintf("%dk", maxrate),
		"-bufsize", fmt.Sprintf("%dk", bufsize),
		"-tag:v", "hvc1",
		"-an", "-sn",
		"-f", "matroska", dst,
	}
}

type vmafLog struct {
	PooledMetrics struct {
		VMAF struct {
			Min          float64 `json:"min"`
			Max          float64 `json:"max"`
			Mean         float64 `json:"mean"`
			HarmonicMean float64 `json:"harmonic_mean"`
		} `json:"vmaf"`
	} `json:"pooled_metrics"`
	Frames []struct {
		Metrics struct {
			VMAF float64 `json:"vmaf"`
		} `json:"metrics"`
	} `json:"frames"`
}

func parseVMAF(data []byte) (VMAFResult, error) {
	var log vmafLog
	if err := json.Unmarshal(data, &log); err != nil {
		return VMAFResult{}, fmt.Errorf("parse vmaf log: %w", err)
	}
	m := log.PooledMetrics.VMAF
	if m.Mean == 0 && len(log.Frames) == 0 {
		return VMAFResult{}, fmt.Errorf("vmaf log has no scores")
	}
	return VMAFResult{
		Mean:     m.Mean,
		Min:      m.Min,
		Max:      m.Max,
		Harmonic: m.HarmonicMean,
		Frames:   len(log.Frames),
	}, nil
}
