// Package planner turns probe facts into a verdict. It is pure: no filesystem,
// no clock, no network. That is what makes "what would you do?" answerable for
// the whole library before a single byte is written.
package planner

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// Verdict is the outcome of the rule list.
type Verdict struct {
	Decision   string // skip | remux | encode | quarantine
	Code       string // stable reason code, e.g. R_HEVC_EFFICIENT
	Reason     string // one sentence, shown in the UI
	TargetKbps int    // set only for encode
}

// State maps a decision onto the file's lifecycle state.
func (v Verdict) State() string {
	if v.Decision == "skip" {
		return "skipped"
	}
	return "planned"
}

// DecideFile applies the container policy and then the rule list.
//
// Only Matroska sources are handled today: 99.4% of the library already is, and
// pretending to support MP4 without testing EAC3-in-MP4 and cover-art handling
// would be worse than saying so. Non-MKV files are skipped with a reason code
// rather than silently processed.
func DecideFile(path string, cfg *config.Config, f media.Facts) Verdict {
	if !strings.EqualFold(filepath.Ext(path), ".mkv") {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		return Verdict{
			Decision: "skip",
			Code:     "R_CONTAINER_UNSUPPORTED",
			Reason:   fmt.Sprintf(".%s source: only Matroska output is supported", ext),
		}
	}
	return Decide(cfg, f)
}

// Decide evaluates the ordered rule list. The last rule is required by config
// validation to be the default rule, so the result is always defined.
func Decide(cfg *config.Config, f media.Facts) Verdict {
	for _, rule := range cfg.Rules {
		if !matches(rule.When, f) {
			continue
		}
		v := Verdict{Decision: rule.Do, Code: rule.Code}
		if rule.Do == "encode" {
			kbps, ok := TargetKbps(cfg, f.Codec, NominalHeight(f.Width, f.Height))
			if !ok {
				// Config validation should make this unreachable; refuse to
				// guess a bitrate rather than silently pick one.
				return Verdict{
					Decision: "skip",
					Code:     "R_NO_TARGET",
					Reason:   fmt.Sprintf("%s at %d kbps has no ladder row for %dpx", codecName(f.Codec), f.BitrateKbps, NominalHeight(f.Width, f.Height)),
				}
			}
			// Two guards keep the planner from spending an hour to make a file
			// bigger. Both were found by running the plan over real media.
			//
			// The rung is a video bitrate, so it is judged against the picture's
			// share: on a low-bitrate source the audio is most of the file, and
			// 1,068 kbps with a 128 kbps track is a 940 kbps picture under a 960
			// kbps rung — an upward encode that used to read as a 10% saving
			// (Trailer Park Boys S01E04, 2026-11-24).
			video := videoShare(f)
			if f.BitrateKbps > 0 && kbps >= video {
				return Verdict{
					Decision: "skip",
					Code:     "R_ALREADY_LEAN",
					Reason: fmt.Sprintf("%s at %d kbps%s is already below the %d kbps target",
						codecName(f.Codec), f.BitrateKbps, videoNote(f, video), kbps),
				}
			}
			// The saving that pays for an encode is file bytes, so the audio
			// that stays is part of the projection: the video shrinks, the
			// track does not.
			if f.BitrateKbps > 0 {
				if pct := projectedSaving(f, kbps); pct < cfg.Quality.MinSaving() {
					return Verdict{
						Decision: "skip",
						Code:     "R_LOW_SAVING",
						Reason: fmt.Sprintf("%s at %d kbps → %d kbps saves only %d%% of the file, under the %d%% floor",
							codecName(f.Codec), f.BitrateKbps, kbps, pct, cfg.Quality.MinSaving()),
					}
				}
			}
			v.TargetKbps = kbps
			v.Reason = encodeReason(f, kbps)
		} else {
			v.Reason = reason(rule.Do, rule.Code, f)
		}
		return v
	}
	return Verdict{Decision: "skip", Code: "R_NO_RULE", Reason: "no rule matched"}
}

func matches(c config.Condition, f media.Facts) bool {
	if c.Default {
		return true
	}
	if c.Codec != "" && c.Codec != f.Codec {
		return false
	}
	if c.HeightLT != nil && f.Height >= *c.HeightLT {
		return false
	}
	if c.HeightGTE != nil && f.Height < *c.HeightGTE {
		return false
	}
	if c.BitrateLT != nil && f.BitrateKbps >= *c.BitrateLT {
		return false
	}
	if c.BitrateGTE != nil && f.BitrateKbps < *c.BitrateGTE {
		return false
	}
	if len(c.AudioCodecIn) > 0 && !anyCodec(c.AudioCodecIn, f.AudioCodecs) {
		return false
	}
	if c.AudioTracksGT != nil && f.AudioTracks <= *c.AudioTracksGT {
		return false
	}
	return true
}

// anyCodec reports whether any of want is among have. An empty have never
// matches: a file with no recorded audio facts must not trip an audio rule.
func anyCodec(want, have []string) bool {
	for _, w := range want {
		for _, h := range have {
			if strings.EqualFold(w, h) {
				return true
			}
		}
	}
	return false
}

// NominalHeight maps a frame to the ladder class it was mastered for.
//
// Height alone is not a resolution. A 2.39:1 film at 1920×800 is a 1080p
// master with a letterbox, and a ladder that prices it by its 800 lines would
// encode it at the 720p rung — 2,700 kbps for 1920-wide picture — and then fail
// its own quality check. Width says which format the source came from; height
// only says how much of the frame is picture.
//
// A narrow frame is the case the width rule gets backwards, and the same
// reasoning fixes it: a 1,436×1,080 4:3 master is 1080p whose width is small
// because the picture is narrow, not because it was mastered small. Read as
// 720p it was priced at 2,700 kbps and scored VMAF 25.5 (Following, 2026-11-17).
// When the aspect is narrower than widescreen by a margin, the height is the
// honest dimension.
func NominalHeight(width, height int) int {
	if width > 0 && height > 0 && float64(width)/float64(height) < narrowAspect {
		return classFromHeight(height)
	}
	switch {
	case width >= 2560:
		return 2160
	case width >= 1700:
		return 1080
	case width >= 1100:
		return 720
	default:
		// SD, or a probe with no width: the real height is the best rung hint
		// there is, and the bottom row of the ladder catches everything.
		return height
	}
}

// narrowAspect is where a frame stops being widescreen: 4:3 (1.33) and 3:2
// (1.50) are narrow, 16:9 (1.78) and every cinematic ratio are not.
const narrowAspect = 1.6

// classFromHeight maps a narrow frame's own height onto the same class
// boundaries the ladder uses, so 4:3 SD stays SD instead of being promoted.
func classFromHeight(height int) int {
	switch {
	case height >= 2000:
		return 2160
	case height >= 1000:
		return 1080
	case height >= 700:
		return 720
	}
	return height
}

// TargetKbps picks the first ladder row for the codec whose threshold the
// class clears. Rows are ordered most-specific-first by validation.
func TargetKbps(cfg *config.Config, codec string, classHeight int) (int, bool) {
	for _, row := range cfg.Quality.Table {
		if row.Codec == codec && classHeight >= row.HeightGTE {
			return row.Kbps, true
		}
	}
	return 0, false
}

// ExpectedVMAF is the quality a ladder rung promises, from calibration.
// Zero means the rung was never measured, and verification will not pretend
// otherwise — an unmeasured rung gets no quality assertion.
func ExpectedVMAF(cfg *config.Config, codec string, classHeight int) float64 {
	for _, row := range cfg.Quality.Table {
		if row.Codec == codec && classHeight >= row.HeightGTE {
			if row.ExpectedVMAF != nil {
				return *row.ExpectedVMAF
			}
			return 0
		}
	}
	return 0
}

func encodeReason(f media.Facts, target int) string {
	return fmt.Sprintf("%s at %d kbps → HEVC %d kbps (−%d%%)",
		codecName(f.Codec), f.BitrateKbps, target, saving(f.BitrateKbps, target))
}

func reason(do, code string, f media.Facts) string {
	switch code {
	case "R_LOW_RES":
		return fmt.Sprintf("%dpx: below 1080p, not worth re-encoding", f.Height)
	case "R_HEVC_EFFICIENT":
		return fmt.Sprintf("HEVC at %d kbps: already efficient", f.BitrateKbps)
	case "R_HEVC_OVERBITRATE":
		return fmt.Sprintf("HEVC at %d kbps: over the threshold", f.BitrateKbps)
	case "R_NO_RULE":
		return fmt.Sprintf("%s: no rule matched", codecName(f.Codec))
	}
	switch do {
	case "remux":
		return fmt.Sprintf("%s: streams only, no re-encode", codecName(f.Codec))
	case "quarantine":
		return "flagged for quarantine"
	}
	return fmt.Sprintf("%s: %s", codecName(f.Codec), code)
}

func saving(from, to int) int {
	if from <= 0 {
		return 0
	}
	pct := (from - to) * 100 / from
	if pct < 0 {
		return 0
	}
	return pct
}

// videoShare is the source's own video bitrate: the file's figure minus the
// audio that stays behind. It falls back to the file's figure when the probe
// did not report per-stream audio bitrates — the container is then all there is,
// and it is what the guards always used.
func videoShare(f media.Facts) int {
	if f.AudioKbps > 0 && f.AudioKbps < f.BitrateKbps {
		return f.BitrateKbps - f.AudioKbps
	}
	return f.BitrateKbps
}

// videoNote is the parenthetical that names the picture's share, for the reason
// line — empty when the two figures are the same number.
func videoNote(f media.Facts, video int) string {
	if video == f.BitrateKbps {
		return ""
	}
	return fmt.Sprintf(" (%d kbps of video)", video)
}

// projectedSaving is the share of file bytes an encode at kbps would return.
// The video shrinks and the audio is copied, so the projection has to count
// both: a rung that lowers the picture is still not worth an encode when the
// file as a whole barely moves.
func projectedSaving(f media.Facts, kbps int) int {
	after := kbps
	if f.AudioKbps > 0 && f.AudioKbps < f.BitrateKbps {
		after += f.AudioKbps
	}
	return saving(f.BitrateKbps, after)
}

func codecName(codec string) string {
	switch strings.ToLower(codec) {
	case "h264":
		return "H.264"
	case "hevc":
		return "HEVC"
	case "av1":
		return "AV1"
	case "mpeg2video":
		return "MPEG-2"
	case "vc1":
		return "VC-1"
	case "vp9":
		return "VP9"
	case "":
		return "unknown codec"
	}
	return strings.ToUpper(codec)
}
