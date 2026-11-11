package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// Problem is one thing wrong with a config, addressed by a JSON-ish path.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError carries every problem found, not just the first, so an agent
// (or a human) can fix the whole file in one pass.
type ValidationError struct {
	Problems []Problem `json:"problems"`
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return fmt.Sprintf("%s: %s", e.Problems[0].Path, e.Problems[0].Message)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d problems:", len(e.Problems))
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p.Path)
		b.WriteString(": ")
		b.WriteString(p.Message)
	}
	return b.String()
}

type problems struct{ items []Problem }

func (p *problems) add(path, format string, args ...any) {
	p.items = append(p.items, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

// AsValidation returns the *ValidationError carried by err, or nil.
func AsValidation(err error) *ValidationError {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve
	}
	return nil
}

// schemaError flattens the validator's tree into one Problem per leaf, using
// the validator's own localised strings so the messages read like English
// ("missing property 'root'") rather than like a stack trace.
func schemaError(err error) error {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return fmt.Errorf("schema: %w", err)
	}
	var ps problems
	flatten(ve, &ps, 0)
	if len(ps.items) == 0 {
		ps.add("/", "%s", ve.Error())
	}
	return &ValidationError{Problems: ps.items}
}

var printer = message.NewPrinter(language.English)

func flatten(ve *jsonschema.ValidationError, ps *problems, depth int) {
	if len(ve.Causes) > 0 && depth < 16 {
		for _, c := range ve.Causes {
			flatten(c, ps, depth+1)
		}
		return
	}
	parent := instancePath(ve.InstanceLocation)
	switch k := ve.ErrorKind.(type) {
	case *kind.Required:
		// One problem per missing key: an agent fixes them one at a time.
		for _, name := range k.Missing {
			ps.add(child(parent, name), "required")
		}
	case *kind.AdditionalProperties:
		for _, name := range k.Properties {
			ps.add(child(parent, name), "unknown key")
		}
	default:
		ps.add(parent, "%s", leafText(ve))
	}
}

// instancePath renders ["rules","0","when"] as /rules/0/when.
func instancePath(loc []string) string {
	if len(loc) == 0 {
		return "/"
	}
	return "/" + strings.Join(loc, "/")
}

func child(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

// leafText prefers the validator's localised rendering, which excludes the
// instance pointer we already carry as Problem.Path.
func leafText(ve *jsonschema.ValidationError) string {
	if ls, ok := ve.ErrorKind.(interface{ LocalizedString(*message.Printer) string }); ok {
		return ls.LocalizedString(printer)
	}
	text := ve.Error()
	if i := strings.LastIndex(text, "\n"); i >= 0 {
		text = text[i+1:]
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "- "))
}

// Check applies the cross-field rules that the JSON Schema cannot express.
func (c *Config) Check() error {
	var ps problems
	c.checkLibraries(&ps)
	c.checkSchedule(&ps)
	c.checkPaths(&ps)
	c.checkQuality(&ps)
	c.checkAudio(&ps)
	c.checkRules(&ps)
	c.checkSafety(&ps)
	if len(ps.items) == 0 {
		return nil
	}
	return &ValidationError{Problems: ps.items}
}

func (c *Config) checkLibraries(ps *problems) {
	seen := map[string]string{}
	roots := map[string]string{}
	for i, lib := range c.Libraries {
		at := fmt.Sprintf("/libraries/%d", i)
		if prev, dup := seen[lib.Name]; dup {
			ps.add(at+"/name", "duplicate library name %q (also %s)", lib.Name, prev)
		}
		seen[lib.Name] = at

		if !filepath.IsAbs(lib.Root) {
			ps.add(at+"/root", "must be an absolute path, got %q", lib.Root)
			continue
		}
		if filepath.Clean(lib.Root) == "/" {
			ps.add(at+"/root", "refusing to scan the filesystem root")
		}
		if prev, dup := roots[lib.Root]; dup {
			ps.add(at+"/root", "duplicate library root (also %s)", prev)
		}
		roots[lib.Root] = at
	}
}

func (c *Config) checkSchedule(ps *problems) {
	if _, err := time.LoadLocation(c.Schedule.Timezone); err != nil {
		ps.add("/schedule/timezone", "unknown IANA timezone %q", c.Schedule.Timezone)
	}
	// Windows are migrated in on load, so an empty list here means the document
	// named neither a window list nor the legacy pair.
	if len(c.Schedule.Windows) == 0 {
		ps.add("/schedule/windows", "the schedule needs at least one window")
		return
	}
	for i, w := range c.Schedule.Windows {
		at := fmt.Sprintf("/schedule/windows/%d", i)
		if len(w.Days) == 0 {
			ps.add(at+"/days", "a window needs at least one day")
		}
		for _, d := range w.Days {
			if DayIndex(d) < 0 {
				ps.add(at+"/days", "%q is not a day (mon…sun)", d)
			}
		}
		r, err := ParseWindow(w.Start + "-" + w.End)
		if err != nil {
			ps.add(at, "%s", err)
			continue
		}
		if r.StartMin == r.EndMin {
			ps.add(at, "a window cannot start and end at the same time")
			continue
		}
		if r.DurationMin() <= c.Schedule.DrainBeforeMinutes {
			ps.add(at, "window %s–%s (%d min) is not longer than drain_before_minutes (%d)",
				w.Start, w.End, r.DurationMin(), c.Schedule.DrainBeforeMinutes)
		}
	}
}

func (c *Config) checkPaths(ps *problems) {
	if c.Paths.Scratch == "" || c.Paths.Quarantine == "" {
		return // schema catches this
	}
	if c.Paths.Scratch == c.Paths.Quarantine {
		ps.add("/paths", "scratch and quarantine must differ")
	}
	if within(c.Paths.Quarantine, c.Paths.Scratch) {
		ps.add("/paths/quarantine", "must not live inside scratch (%s)", c.Paths.Scratch)
	}
	if within(c.Paths.Scratch, c.Paths.Quarantine) {
		ps.add("/paths/scratch", "must not live inside quarantine (%s)", c.Paths.Quarantine)
	}
	for i, lib := range c.Libraries {
		if within(c.Paths.Scratch, lib.Root) {
			ps.add("/paths/scratch", "must not live inside library %q (%s): the scanner would ingest temp files", lib.Name, lib.Root)
		}
		if within(c.Paths.Quarantine, lib.Root) {
			ps.add("/paths/quarantine", "must not live inside library %q (%s): quarantined files would be rescanned", lib.Name, lib.Root)
		}
		_ = i
	}
}

func (c *Config) checkQuality(ps *problems) {
	last := map[string]int{}
	covered := map[string]bool{}
	for i, row := range c.Quality.Table {
		at := fmt.Sprintf("/quality/table/%d", i)
		if prev, ok := last[row.Codec]; ok && row.HeightGTE >= prev {
			ps.add(at+"/height_gte", "rows must be most-specific-first: %d must be lower than the previous %s row (%d)",
				row.HeightGTE, row.Codec, prev)
		}
		last[row.Codec] = row.HeightGTE
		if row.HeightGTE <= 1080 {
			covered[row.Codec] = true
		}
	}
	for _, codec := range []string{"h264", "hevc"} {
		if last[codec] == 0 {
			continue // codec not used by the table at all
		}
		if !covered[codec] {
			ps.add("/quality/table", "%s rows never cover 1080p: add a row with height_gte <= 1080", codec)
		}
	}
}

func (c *Config) checkAudio(ps *problems) {
	for i, t := range c.Audio.Transcode {
		at := fmt.Sprintf("/audio/transcode/%d", i)
		for _, from := range t.From {
			if strings.EqualFold(from, t.To) {
				ps.add(at+"/from", "source codec %q equals the target %q, which would re-encode lossy audio", from, t.To)
			}
		}
	}
}

func (c *Config) checkRules(ps *problems) {
	codes := map[string]int{}
	for i, rule := range c.Rules {
		at := fmt.Sprintf("/rules/%d", i)
		if prev, dup := codes[rule.Code]; dup {
			ps.add(at+"/code", "duplicate rule code %q (also /rules/%d)", rule.Code, prev)
		}
		codes[rule.Code] = i

		w := rule.When
		if w.Default {
			if w.HeightLT != nil || w.HeightGTE != nil || w.BitrateLT != nil || w.BitrateGTE != nil ||
				w.Codec != "" || len(w.AudioCodecIn) > 0 || w.AudioTracksGT != nil {
				ps.add(at+"/when", "the default rule cannot carry other conditions")
			}
			continue
		}
		if w.HeightLT == nil && w.HeightGTE == nil && w.BitrateLT == nil && w.BitrateGTE == nil &&
			w.Codec == "" && len(w.AudioCodecIn) == 0 && w.AudioTracksGT == nil {
			ps.add(at+"/when", "no conditions: use \"default\": true instead")
		}
		if w.HeightLT != nil && w.HeightGTE != nil && *w.HeightLT <= *w.HeightGTE {
			ps.add(at+"/when/height_lt", "height_lt %d can never exceed height_gte %d", *w.HeightLT, *w.HeightGTE)
		}
		if w.BitrateLT != nil && w.BitrateGTE != nil && *w.BitrateLT <= *w.BitrateGTE {
			ps.add(at+"/when/bitrate_kbps_lt", "bitrate_kbps_lt %d can never exceed bitrate_kbps_gte %d", *w.BitrateLT, *w.BitrateGTE)
		}
		if rule.Do == "encode" && w.Codec != "" && !c.tableHasCodec(w.Codec) {
			ps.add(at+"/when/codec", "rule encodes %s but /quality/table has no %s rows", w.Codec, w.Codec)
		}
	}
	if n := len(c.Rules); n > 0 && !c.Rules[n-1].When.Default {
		ps.add(fmt.Sprintf("/rules/%d", n-1), "the last rule must be the default rule, so every file has a decision")
	}
}

func (c *Config) tableHasCodec(codec string) bool {
	for _, row := range c.Quality.Table {
		if row.Codec == codec {
			return true
		}
	}
	return false
}

func (c *Config) checkSafety(ps *problems) {
	if c.Safety.SizeRatioMin >= c.Safety.SizeRatioMax {
		ps.add("/safety/size_ratio_min", "must be lower than size_ratio_max (%v)", c.Safety.SizeRatioMax)
	}
}

// within reports whether a path is inside dir (or equal to it).
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Window is a time-of-day range in minutes since midnight. Start may be later
// than End, which means the window wraps past midnight.
type Window struct {
	StartMin int
	EndMin   int
}

// ParseWindow parses "HH:MM-HH:MM".
func ParseWindow(s string) (Window, error) {
	start, end, ok := strings.Cut(s, "-")
	if !ok {
		return Window{}, fmt.Errorf("window %q must look like \"00:30-05:30\"", s)
	}
	a, err := parseClock(start)
	if err != nil {
		return Window{}, fmt.Errorf("window %q: %w", s, err)
	}
	b, err := parseClock(end)
	if err != nil {
		return Window{}, fmt.Errorf("window %q: %w", s, err)
	}
	// "22:00-00:00" is two hours, not twenty-six: a midnight end means the end
	// of that same day. Without this, the only way to say "until midnight" is
	// the 23:59 convention, and a schedule grid that ends a block at 24:00 would
	// produce a window that wraps a full day.
	if b == 0 && a > 0 {
		b = 24 * 60
	}
	return Window{StartMin: a, EndMin: b}, nil
}

func parseClock(s string) (int, error) {
	hh, mm, ok := strings.Cut(s, ":")
	if !ok {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("%q has an out-of-range hour", s)
	}
	m, err := strconv.Atoi(mm)
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("%q has an out-of-range minute", s)
	}
	return h*60 + m, nil
}

// DurationMin is the window length in minutes, wrapping if needed.
func (w Window) DurationMin() int {
	if w.EndMin >= w.StartMin {
		return w.EndMin - w.StartMin
	}
	return 24*60 - w.StartMin + w.EndMin
}

// Contains reports whether t's wall clock falls inside the window.
func (w Window) Contains(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	if w.EndMin >= w.StartMin {
		return m >= w.StartMin && m < w.EndMin
	}
	return m >= w.StartMin || m < w.EndMin
}

// MinutesUntilEnd is how long the window has left at t; negative when closed.
func (w Window) MinutesUntilEnd(t time.Time) int {
	m := t.Hour()*60 + t.Minute()
	if !w.Contains(t) {
		return -1
	}
	if w.EndMin >= w.StartMin {
		return w.EndMin - m
	}
	if m >= w.StartMin {
		return 24*60 - m + w.EndMin
	}
	return w.EndMin - m
}
