// Package configdiff answers "what would this save change?" without applying it.
//
// It is structural, not textual: a screen that shows forty changed lines for a
// reordered rule list has not told the operator anything. Arrays are compared
// index-wise, except when both sides hold the same elements in a different order
// — then it is one reorder, which is what it is.
package configdiff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Kinds of change.
const (
	Added     = "add"
	Removed   = "remove"
	Changed   = "change"
	Reordered = "reorder"
)

// Change is one difference, addressed by a JSON pointer.
type Change struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Group  string `json:"group"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// Summary counts what happened.
type Summary struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Changed   int `json:"changed"`
	Reordered int `json:"reordered"`
}

// Group is one section of the document that changed.
type Group struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Diff is everything the confirmation dialog needs.
type Diff struct {
	Summary Summary  `json:"summary"`
	Groups  []Group  `json:"groups"`
	Changes []Change `json:"changes"`
	// Text is a unified diff of the two pretty-printed documents, for the
	// "raw" toggle. It is derived, never the primary reading.
	Text string `json:"text"`
}

// Empty reports whether nothing changed.
func (d Diff) Empty() bool {
	return d.Summary.Added == 0 && d.Summary.Removed == 0 && d.Summary.Changed == 0 && d.Summary.Reordered == 0
}

// Compute diffs two JSON documents. Either side may be empty (no revision yet).
func Compute(before, after []byte) (Diff, error) {
	// An absent side is an empty document, not a scalar: a first save is "every
	// section was added", which is what a person would say.
	a, b := any(map[string]any{}), any(map[string]any{})
	if len(before) > 0 {
		if err := json.Unmarshal(before, &a); err != nil {
			return Diff{}, fmt.Errorf("before: %w", err)
		}
	}
	if len(after) > 0 {
		if err := json.Unmarshal(after, &b); err != nil {
			return Diff{}, fmt.Errorf("after: %w", err)
		}
	}

	var changes []Change
	walk("", a, b, &changes)

	d := Diff{Changes: changes}
	groups := map[string]*Group{}
	for _, c := range changes {
		switch c.Kind {
		case Added:
			d.Summary.Added++
		case Removed:
			d.Summary.Removed++
		case Reordered:
			d.Summary.Reordered++
		default:
			d.Summary.Changed++
		}
		g, ok := groups[c.Group]
		if !ok {
			g = &Group{Key: c.Group, Label: groupLabel(c.Group)}
			groups[c.Group] = g
		}
		g.Count++
	}
	for _, g := range groups {
		d.Groups = append(d.Groups, *g)
	}
	sort.Slice(d.Groups, func(i, j int) bool { return d.Groups[i].Key < d.Groups[j].Key })

	d.Text = unified(pretty(before), pretty(after))
	return d, nil
}

func walk(path string, a, b any, out *[]Change) {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			addChange(path, a, b, out)
			return
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		ordered := make([]string, 0, len(keys))
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			p := path + "/" + k
			avv, aok := av[k]
			bvv, bok := bv[k]
			switch {
			case !bok:
				*out = append(*out, Change{Path: p, Kind: Removed, Before: avv,
					Label: label(p, avv), Group: group(p)})
			case !aok:
				*out = append(*out, Change{Path: p, Kind: Added, After: bvv,
					Label: label(p, bvv), Group: group(p)})
			default:
				walk(p, avv, bvv, out)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok {
			addChange(path, a, b, out)
			return
		}
		// Same elements, order moved: one fact, not N.
		if len(av) == len(bv) && sameElements(av, bv) {
			if len(av) > 0 && !reflect.DeepEqual(av, bv) {
				*out = append(*out, Change{Path: path, Kind: Reordered,
					Label: "the list was reordered", Group: group(path), After: len(bv)})
			}
			return
		}
		n := len(av)
		if len(bv) > n {
			n = len(bv)
		}
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("%s/%d", path, i)
			switch {
			case i >= len(bv):
				*out = append(*out, Change{Path: p, Kind: Removed, Before: av[i],
					Label: label(p, av[i]), Group: group(p)})
			case i >= len(av):
				*out = append(*out, Change{Path: p, Kind: Added, After: bv[i],
					Label: label(p, bv[i]), Group: group(p)})
			default:
				walk(p, av[i], bv[i], out)
			}
		}
	default:
		if !reflect.DeepEqual(a, b) {
			addChange(path, a, b, out)
		}
	}
}

func addChange(path string, a, b any, out *[]Change) {
	*out = append(*out, Change{Path: path, Kind: Changed, Label: label(path, a), Group: group(path),
		Before: a, After: b})
}

// sameElements compares two arrays as multisets of their canonical encoding.
func sameElements(a, b []any) bool {
	key := func(v []any) []string {
		out := make([]string, len(v))
		for i, e := range v {
			raw, err := json.Marshal(e)
			if err != nil {
				raw = []byte(fmt.Sprintf("%v", e))
			}
			out[i] = string(raw)
		}
		sort.Strings(out)
		return out
	}
	ka, kb := key(a), key(b)
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

// group is the top-level key a change belongs to.
func group(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "root"
	}
	return parts[0]
}

var groupLabels = map[string]string{
	"libraries": "Libraries",
	"schedule":  "Schedule",
	"workers":   "Workers",
	"paths":     "Paths",
	"quality":   "Quality",
	"safety":    "Safety",
	"audio":     "Audio",
	"subtitles": "Subtitles",
	"rules":     "Rules",
	"notify":    "Notify",
	"server":    "Server",
	"root":      "Document",
}

func groupLabel(key string) string {
	if l, ok := groupLabels[key]; ok {
		return l
	}
	return key
}

// fieldLabels names the keys worth naming. Everything else falls back to the
// path, which is still readable ("quality/table/2/kbps").
var fieldLabels = map[string]string{
	"start":                   "start",
	"end":                     "end",
	"days":                    "days",
	"timezone":                "timezone",
	"drain_before_minutes":    "drain before the window closes",
	"mode":                    "mode",
	"vmaf_target":             "VMAF target",
	"vmaf_tolerance":          "VMAF tolerance",
	"min_saving_pct":          "smallest saving worth an encode",
	"keep_quarantine_days":    "quarantine keep window",
	"min_free_gb":             "disk floor",
	"size_ratio_max":          "largest allowed growth",
	"verify_vmaf":             "verify VMAF",
	"max_attempts":            "attempt budget",
	"quarantine":              "quarantine mode",
	"height_gte":              "from height",
	"kbps":                    "bitrate",
	"expected_vmaf":           "expected VMAF",
	"codec":                   "codec",
	"do":                      "decision",
	"code":                    "code",
	"encode":                  "encode workers",
	"probe":                   "probe workers",
	"scratch":                 "scratch path",
	"root":                    "root path",
	"listen":                  "listen address",
	"url":                     "server URL",
	"jellyfin_refresh":        "refresh Jellyfin",
	"on":                      "notify on",
	"keep_languages":          "keep languages",
	"keep_original_language":  "keep the original language",
	"max_tracks":              "maximum tracks",
	"drop_commentary":         "drop commentary",
	"drop_closed_captions":    "drop closed captions",
	"keep_forced":             "keep forced subtitles",
	"radius":                  "radius",
	"token":                   "token",
	"version":                 "version",
	"transcode":               "audio conversion",
	"from":                    "from codecs",
	"to":                      "target codec",
	"table":                   "bitrate ladder",
	"rules":                   "rules",
	"windows":                 "windows",
	"when":                    "condition",
	"bitrate_kbps_lt":         "bitrate below",
	"bitrate_kbps_gte":        "bitrate at least",
	"height_lt":               "below height",
	"audio_codec_in":          "audio codec in",
	"audio_tracks_gt":         "more audio tracks than",
	"default":                 "default rule",
	"verify_sample_seconds":   "verification sample",
	"probe_outliers_above_kbps": "outlier probe above",
	"ingest_buffer":           "ingest buffer",
	"extensions":              "extensions",
	"exclude":                 "excluded paths",
	"name":                    "name",
	"radarr":                  "Radarr URL",
	"sonarr":                  "Sonarr URL",
	"size_ratio_min":          "smallest allowed ratio",
	"duration_tolerance_s":    "duration tolerance",
	"libraries":               "libraries",
	"paths":                   "paths",
}

// label turns a path into something readable, keeping the index it points at.
func label(path string, _ any) string {
	if path == "" {
		return "the document"
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	last := parts[len(parts)-1]
	name, ok := fieldLabels[last]
	if !ok {
		name = last
	}
	var idx []string
	for _, p := range parts[:len(parts)-1] {
		if isIndex(p) {
			idx = append(idx, p)
		}
	}
	if len(idx) > 0 {
		return fmt.Sprintf("%s · #%s", title(group(path)), strings.Join(idx, "."))
	}
	return fmt.Sprintf("%s · %s", title(group(path)), name)
}

func isIndex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func title(s string) string {
	if s == "" {
		return "Document"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// pretty re-indents a JSON document for the raw diff; invalid JSON is passed
// through so the diff still says something.
func pretty(doc []byte) string {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return string(doc)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(doc)
	}
	return string(out)
}
