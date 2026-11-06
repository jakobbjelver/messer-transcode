package ffmpeg

import (
	"fmt"
	"strconv"
)

// AudioSpec is one audio stream in the output.
type AudioSpec struct {
	Index       int    // input stream index
	TranscodeTo string // "" copies the stream
	Kbps        int
}

// EncodeSpec is everything Build needs. It deliberately carries no planner or
// config types: the encoder is a dumb command builder.
type EncodeSpec struct {
	Src        string
	Dst        string
	VideoIndex int
	Audio      []AudioSpec
	Subs       []int
	// TargetKbps is the video bitrate; ignored when VideoCopy is true.
	TargetKbps int
	VideoCopy  bool
}

// Build returns the argument vector for one pass. The argument order is
// deliberate and covered by golden tests: options before -i apply to the input,
// options after apply to the output.
func Build(spec EncodeSpec) []string {
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-nostats",
		"-progress", "pipe:1",
		"-i", spec.Src,
	}

	args = append(args, "-map", fmt.Sprintf("0:%d", spec.VideoIndex))
	for _, a := range spec.Audio {
		args = append(args, "-map", fmt.Sprintf("0:%d", a.Index))
	}
	for _, s := range spec.Subs {
		args = append(args, "-map", fmt.Sprintf("0:%d", s))
	}

	if spec.VideoCopy {
		args = append(args, "-c:v", "copy")
	} else {
		// VideoToolbox ignores -qp entirely, so the rate is set with -b:v and
		// bounded with maxrate/bufsize to stop it overshooting on hard scenes.
		bitrate := spec.TargetKbps
		maxrate := bitrate * 3 / 2
		bufsize := maxrate * 2
		args = append(args,
			"-c:v", "hevc_videotoolbox",
			"-b:v", fmt.Sprintf("%dk", bitrate),
			"-maxrate", fmt.Sprintf("%dk", maxrate),
			"-bufsize", fmt.Sprintf("%dk", bufsize),
			"-tag:v", "hvc1",
		)
	}

	// Audio is addressed by output position, not input index, because the map
	// list above may have reordered or dropped streams.
	for i, a := range spec.Audio {
		if a.TranscodeTo == "" {
			args = append(args, fmt.Sprintf("-c:a:%d", i), "copy")
			continue
		}
		args = append(args,
			fmt.Sprintf("-c:a:%d", i), a.TranscodeTo,
			fmt.Sprintf("-b:a:%d", i), fmt.Sprintf("%dk", a.Kbps),
		)
	}

	if len(spec.Subs) > 0 {
		args = append(args, "-c:s", "copy")
	}

	args = append(args,
		"-map_metadata", "0",
		"-map_chapters", "0",
		"-f", "matroska",
		spec.Dst,
	)
	return args
}

// ProbeArgs is the ffprobe invocation used before and after a transcode.
func ProbeArgs(path string) []string {
	return []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"--", path,
	}
}

// parseSpeed turns "12.3x" into 12.3.
func parseSpeed(v string) float64 {
	if len(v) == 0 {
		return 0
	}
	if v[len(v)-1] == 'x' {
		v = v[:len(v)-1]
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}
