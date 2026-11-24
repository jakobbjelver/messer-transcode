package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// videoEndWindow is how far back from the container's end VideoEnd reads. A
// trailing subtitle or audio tail is tens of seconds; five minutes is generous
// and still costs one seek rather than one read of the file.
const videoEndWindow = 300

// videoEndTimeout bounds the seek. A file without an index makes ffprobe read
// from the start, and a verification step may not stall a job for that.
const videoEndTimeout = 30 * time.Second

// VideoEnd measures where the picture ends: the last video packet's
// presentation time plus its duration.
//
// The container's duration is not the film's. A WEB-DL whose subtitle track
// runs past the last video frame reports a duration the encode can never reach,
// and a verifier that compares against it rejects a correct file — measured
// 2026-11-24 on Vertigo, whose video ends at 7693.35s, whose audio ends at
// 7693.38s, and whose only packet past that is a subtitle at 7723.42s.
//
// Zero means "not measured": no video stream, a failed probe, or a timeout. The
// caller falls back to the container rather than guessing.
func VideoEnd(ctx context.Context, path string, containerS float64) float64 {
	if containerS <= 0 || path == "" {
		return 0
	}
	start := 0.0
	if containerS > videoEndWindow {
		start = containerS - videoEndWindow
	}

	ctx, cancel := context.WithTimeout(ctx, videoEndTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-read_intervals", strconv.FormatFloat(start, 'f', 3, 64),
		"-show_entries", "packet=pts_time,duration_time",
		"-of", "csv=p=0",
		"--", path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return 0
	}

	end := parsePacketEnds(&out)
	if end <= 0 {
		return 0
	}
	// A frame that ends after the file claims to stops at the file.
	if end > containerS {
		end = containerS
	}
	return end
}

// parsePacketEnds returns the last frame end from ffprobe's CSV of
// pts_time,duration_time. Packets arrive in decode order, not presentation
// order, so the maximum — not the last line — is the end. A missing duration
// (ffprobe omits it for some streams) falls back to the presentation time.
func parsePacketEnds(r io.Reader) float64 {
	var max float64
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Split(strings.TrimSpace(sc.Text()), ",")
		if len(fields) == 0 || fields[0] == "" {
			continue
		}
		pts, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		end := pts
		if len(fields) > 1 {
			if d, err := strconv.ParseFloat(fields[1], 64); err == nil {
				end += d
			}
		}
		if end > max {
			max = end
		}
	}
	return max
}
