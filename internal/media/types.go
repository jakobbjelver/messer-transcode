// Package media holds the facts a probe produces and a decision consumes.
// It has no dependencies so the planner, the store and the prober can all
// agree on one shape.
package media

import "strings"

// AudioTrack is one audio stream as it exists in the file.
type AudioTrack struct {
	Index      int    `json:"index"`
	Codec      string `json:"codec"`
	Lang       string `json:"lang"`
	Channels   int    `json:"channels"`
	Title      string `json:"title,omitempty"`
	Commentary bool   `json:"commentary,omitempty"`
	Default    bool   `json:"default,omitempty"`
}

// SubTrack is one subtitle stream.
type SubTrack struct {
	Index           int    `json:"index"`
	Codec           string `json:"codec"`
	Lang            string `json:"lang"`
	Title           string `json:"title,omitempty"`
	Forced          bool   `json:"forced,omitempty"`
	Commentary      bool   `json:"commentary,omitempty"`
	HearingImpaired bool   `json:"hearing_impaired,omitempty"`
}

// Scanned is one file as the filesystem reports it, before probing.
type Scanned struct {
	Path    string
	Size    int64
	MtimeNS int64
}

// Probe is everything we know about a file before deciding what to do with it.
type Probe struct {
	Container   string       `json:"container"`
	VideoCodec  string       `json:"video_codec"`
	Width       int          `json:"width"`
	Height      int          `json:"height"`
	PixFmt      string       `json:"pix_fmt"`
	DurationS   float64      `json:"duration_s"`
	SizeBytes   int64        `json:"size_bytes"`
	BitrateKbps int          `json:"bitrate_kbps"`
	Audio       []AudioTrack `json:"audio"`
	Subs        []SubTrack   `json:"subs"`
}

// Facts are the fields a decision rule can read.
type Facts struct {
	Height      int
	Codec       string
	BitrateKbps int
}

// Facts projects a probe down to what the rules engine needs.
func (p Probe) Facts() Facts {
	return Facts{Height: p.Height, Codec: p.VideoCodec, BitrateKbps: p.BitrateKbps}
}

// IsCommentary reports whether a free-text track title marks commentary.
func IsCommentary(title string) bool {
	return strings.Contains(strings.ToLower(title), "commentary")
}

// IsHearingImpaired reports whether a subtitle title marks SDH or closed
// captions. Matching "CC" as a bare token risks false positives ("Picard"),
// so the phrase must be explicit or the token must stand alone.
func IsHearingImpaired(title string) bool {
	t := strings.ToLower(title)
	if strings.Contains(t, "sdh") ||
		strings.Contains(t, "hearing impaired") ||
		strings.Contains(t, "closed caption") {
		return true
	}
	for _, field := range strings.FieldsFunc(t, func(r rune) bool {
		return !(r >= 'a' && r <= 'z')
	}) {
		if field == "cc" {
			return true
		}
	}
	return false
}

// NormaliseLang maps the values ffprobe actually emits onto ISO 639-2/B.
func NormaliseLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "", "und", "unknown":
		return "und"
	case "en", "eng":
		return "eng"
	case "sv", "swe":
		return "swe"
	case "da", "dan":
		return "dan"
	case "no", "nor", "nb", "nob":
		return "nor"
	}
	if len(lang) > 3 {
		return lang[:3]
	}
	return lang
}
