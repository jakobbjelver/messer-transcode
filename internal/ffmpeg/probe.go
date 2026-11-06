// Package ffmpeg wraps the two binaries we depend on. It never builds a shell
// command: arguments are passed as a slice, so a filename with a quote in it is
// just a filename.
package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// Error codes this package can produce.
const (
	ErrFFProbe = "E_PROBE_FFPROBE"
)

// ProbeError carries the stable code plus the tool's own words.
type ProbeError struct {
	Code   string
	Path   string
	Stderr string
	Err    error
}

func (e *ProbeError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Path, msg)
}

func (e *ProbeError) Unwrap() error { return e.Err }

// Probe runs ffprobe and maps the result onto media.Probe.
func Probe(ctx context.Context, path string) (media.Probe, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-show_error",
		"--", path,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return media.Probe{}, &ProbeError{Code: ErrFFProbe, Path: path, Stderr: stderr.String(), Err: err}
	}
	p, err := Parse(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return media.Probe{}, &ProbeError{Code: ErrFFProbe, Path: path, Stderr: err.Error()}
	}
	return p, nil
}

type rawProbe struct {
	Streams []rawStream `json:"streams"`
	Format  rawFormat   `json:"format"`
}

type rawStream struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	PixFmt      string            `json:"pix_fmt"`
	Channels    int               `json:"channels"`
	Duration    string            `json:"duration"`
	Tags        map[string]string `json:"tags"`
	Disposition struct {
		Default         int `json:"default"`
		Forced          int `json:"forced"`
		Comment         int `json:"comment"`
		HearingImpaired int `json:"hearing_impaired"`
		AttachedPic     int `json:"attached_pic"`
	} `json:"disposition"`
}

type rawFormat struct {
	FormatName string `json:"format_name"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
}

// Parse turns ffprobe's JSON into media.Probe. Pure: no process, no filesystem.
func Parse(r *bytes.Reader) (media.Probe, error) {
	var raw rawProbe
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return media.Probe{}, fmt.Errorf("decode ffprobe output: %w", err)
	}
	return FromRaw(raw)
}

// FromRaw is Parse without the JSON step, for tests.
func FromRaw(raw rawProbe) (media.Probe, error) {
	p := media.Probe{Container: cleanContainer(raw.Format.FormatName)}

	duration, _ := strconv.ParseFloat(raw.Format.Duration, 64)
	size, _ := strconv.ParseInt(raw.Format.Size, 10, 64)
	formatBitrate, _ := strconv.ParseInt(raw.Format.BitRate, 10, 64)

	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			// Cover art is a video stream too; it is not the feature.
			if s.Disposition.AttachedPic == 1 || p.VideoCodec != "" {
				continue
			}
			p.VideoCodec = s.CodecName
			p.VideoIndex = s.Index
			p.Width = s.Width
			p.Height = s.Height
			p.PixFmt = s.PixFmt
			if duration == 0 {
				duration, _ = strconv.ParseFloat(s.Duration, 64)
			}
		case "audio":
			p.Audio = append(p.Audio, media.AudioTrack{
				Index:      s.Index,
				Codec:      s.CodecName,
				Lang:       media.NormaliseLang(s.Tags["language"]),
				Channels:   s.Channels,
				Title:      strings.TrimSpace(s.Tags["title"]),
				Commentary: s.Disposition.Comment == 1 || media.IsCommentary(s.Tags["title"]),
				Default:    s.Disposition.Default == 1,
			})
		case "subtitle":
			p.Subs = append(p.Subs, media.SubTrack{
				Index:           s.Index,
				Codec:           s.CodecName,
				Lang:            media.NormaliseLang(s.Tags["language"]),
				Title:           strings.TrimSpace(s.Tags["title"]),
				Forced:          s.Disposition.Forced == 1,
				Commentary:      s.Disposition.Comment == 1 || media.IsCommentary(s.Tags["title"]),
				HearingImpaired: s.Disposition.HearingImpaired == 1 || media.IsHearingImpaired(s.Tags["title"]),
			})
		}
	}

	p.DurationS = duration
	p.SizeBytes = size

	// Matroska usually omits format.bit_rate, so derive it from the real size
	// and duration. That is also the number that matches "what this costs on
	// disk", which is what the bitrate rules are really about.
	switch {
	case size > 0 && duration > 0:
		p.BitrateKbps = int(float64(size) * 8 / duration / 1000)
	case formatBitrate > 0:
		p.BitrateKbps = int(formatBitrate / 1000)
	}

	if p.VideoCodec == "" {
		return p, fmt.Errorf("no video stream found")
	}
	return p, nil
}

// cleanContainer keeps the first name from "matroska,webm".
func cleanContainer(name string) string {
	if i := strings.IndexByte(name, ','); i >= 0 {
		return name[:i]
	}
	return name
}
