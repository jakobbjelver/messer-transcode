package planner

import (
	"sort"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// AudioChoice is one retained audio stream and what happens to it.
type AudioChoice struct {
	Index       int
	Codec       string
	Lang        string
	TranscodeTo string // "" means copy
	Kbps        int
}

// StreamPlan is the exact set of streams the output will contain.
type StreamPlan struct {
	VideoIndex int
	Audio      []AudioChoice
	Subs       []int
}

// SelectStreams applies the audio and subtitle policy to a probe. originalLang
// is the film's own language (from TMDB via Radarr/Sonarr); pass "" when
// unknown, in which case only the configured languages are kept.
func SelectStreams(p media.Probe, cfg *config.Config, originalLang string) StreamPlan {
	sp := StreamPlan{VideoIndex: p.VideoIndex}
	sp.Audio = selectAudio(p.Audio, cfg.Audio, originalLang)
	sp.Subs = selectSubs(p.Subs, cfg.Subtitles)
	return sp
}

func selectAudio(tracks []media.AudioTrack, cfg config.Audio, originalLang string) []AudioChoice {
	type candidate struct {
		track media.AudioTrack
		rank  int
	}
	var candidates []candidate

	for _, t := range tracks {
		if cfg.DropCommentary && t.Commentary {
			continue
		}
		rank, keep := audioRank(t, cfg, originalLang)
		if !keep {
			continue
		}
		candidates = append(candidates, candidate{track: t, rank: rank})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return candidates[i].track.Index < candidates[j].track.Index
	})

	limitTracks := cfg.MaxTracks
	if limitTracks <= 0 {
		limitTracks = len(candidates)
	}
	// Safety valve: a language policy that matches nothing must not strip every
	// track from a film. Fall back to the default track, then the first one.
	if len(candidates) == 0 {
		for _, t := range tracks {
			if !t.Commentary {
				candidates = append(candidates, candidate{track: t, rank: 0})
				break
			}
		}
	}
	if len(candidates) == 0 && len(tracks) > 0 {
		candidates = append(candidates, candidate{track: tracks[0], rank: 0})
	}
	if len(candidates) > limitTracks {
		candidates = candidates[:limitTracks]
	}

	out := make([]AudioChoice, 0, len(candidates))
	for _, c := range candidates {
		choice := AudioChoice{
			Index: c.track.Index,
			Codec: c.track.Codec,
			Lang:  c.track.Lang,
		}
		if target, kbps, ok := transcodeFor(c.track.Codec, cfg.Transcode); ok {
			choice.TranscodeTo = target
			choice.Kbps = kbps
		}
		out = append(out, choice)
	}
	return out
}

// audioRank returns a preference rank and whether to keep the track. Lower
// ranks are kept first: the film's own language, then the configured order.
func audioRank(t media.AudioTrack, cfg config.Audio, originalLang string) (int, bool) {
	if len(cfg.KeepLanguages) == 0 && !cfg.KeepOriginalLanguage {
		return 0, true
	}
	if cfg.KeepOriginalLanguage && originalLang != "" && t.Lang == originalLang {
		return 0, true
	}
	for i, lang := range cfg.KeepLanguages {
		if t.Lang == lang {
			return 1 + i, true
		}
	}
	return 0, false
}

func transcodeFor(codec string, rules []config.AudioTranscode) (string, int, bool) {
	for _, r := range rules {
		for _, from := range r.From {
			if equalFold(from, codec) {
				return r.To, r.Kbps, true
			}
		}
	}
	return "", 0, false
}

func selectSubs(tracks []media.SubTrack, cfg config.Subtitles) []int {
	var out []int
	for _, s := range tracks {
		if cfg.DropCommentary && s.Commentary {
			continue
		}
		forced := s.Forced && cfg.KeepForced
		if cfg.DropClosedCaptions && s.HearingImpaired && !forced {
			continue
		}
		if forced || containsString(cfg.KeepLanguages, s.Lang) {
			out = append(out, s.Index)
		}
	}
	return out
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
