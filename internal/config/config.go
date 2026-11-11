// Package config defines the messer-transcode configuration format.
//
// schema.json is the single source of truth for shape, types, enums and
// ranges; Check adds the cross-field rules a JSON Schema cannot express
// (window ordering, table specificity, rule totality, path containment).
//
// Validation is deliberately strict: an unknown key is an error, because a
// silently ignored typo in this file is the difference between "encode this
// tier" and "encode everything".
package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.json
var schemaJSON []byte

// Config is a complete config.json.
type Config struct {
	Version   int       `json:"version"`
	Libraries []Library `json:"libraries"`
	Schedule  Schedule  `json:"schedule"`
	Workers   Workers   `json:"workers"`
	Paths     Paths     `json:"paths"`
	Quality   Quality   `json:"quality"`
	Audio     Audio     `json:"audio"`
	Subtitles Subtitles `json:"subtitles"`
	Rules     []Rule    `json:"rules"`
	Safety    Safety    `json:"safety"`
	Notify    Notify    `json:"notify"`
	Server    Server    `json:"server"`
}

// Library is one root that the node walks.
type Library struct {
	Name       string   `json:"name"`
	Root       string   `json:"root"`
	Extensions []string `json:"extensions"`
	Exclude    []string `json:"exclude,omitempty"`
}

// Schedule defines when the node may run jobs, in the configured timezone.
//
// Windows are the source of truth. Weekday/Weekend are the pre-windows format:
// they are still accepted (an old file, or a hand-written one) and migrated into
// two windows on load, then dropped — one representation reaches the rest of
// the program.
type Schedule struct {
	Windows            []ScheduleWindow `json:"windows,omitempty"`
	Weekday            string           `json:"weekday,omitempty"`
	Weekend            string           `json:"weekend,omitempty"`
	DrainBeforeMinutes int              `json:"drain_before_minutes"`
	Timezone           string           `json:"timezone"`
}

// Workers bounds concurrency. Encode above 1 buys no throughput on Apple
// silicon: the media engine is serial (see docs/architecture.md).
type Workers struct {
	Encode       int `json:"encode"`
	Probe        int `json:"probe"`
	IngestBuffer int `json:"ingest_buffer"`
}

// Paths are absolute, node-local directories.
type Paths struct {
	Scratch    string `json:"scratch"`
	Quarantine string `json:"quarantine"`
}

// Quality picks the target bitrate for an encode.
type Quality struct {
	Mode               string       `json:"mode"`
	VMAFTarget         float64      `json:"vmaf_target"`
	VMAFTolerance      float64      `json:"vmaf_tolerance"`
	Table              []QualityRow `json:"table"`
	ProbeOutliersAbove int          `json:"probe_outliers_above_kbps"`
	// MinSavingPct is the smallest projected size reduction worth spending an
	// encode on. Absent means 10 percent; an explicit 0 disables the floor.
	MinSavingPct *int `json:"min_saving_pct,omitempty"`
}

// MinSaving resolves the saving floor.
func (q Quality) MinSaving() int {
	if q.MinSavingPct == nil {
		return 10
	}
	return *q.MinSavingPct
}

// QualityRow is one rung of the bitrate ladder. Rows are matched
// most-specific-first: the first row whose codec matches and whose
// height_gte is <= the file's height wins.
type QualityRow struct {
	Codec     string `json:"codec"`
	HeightGTE int    `json:"height_gte"`
	Kbps      int    `json:"kbps"`
	// ExpectedVMAF is what calibration measured this rung to deliver on this
	// library. Verification compares against it when set, because a single
	// global target is unreachable for some content (grain never survives a
	// re-encode, at any bitrate).
	ExpectedVMAF *float64 `json:"expected_vmaf,omitempty"`
}

// Audio controls track retention and lossless-to-lossy conversion.
type Audio struct {
	KeepLanguages        []string         `json:"keep_languages,omitempty"`
	KeepOriginalLanguage bool             `json:"keep_original_language"`
	MaxTracks            int              `json:"max_tracks"`
	DropCommentary       bool             `json:"drop_commentary"`
	Transcode            []AudioTranscode `json:"transcode,omitempty"`
}

// AudioTranscode converts a set of source codecs to one target codec.
type AudioTranscode struct {
	From []string `json:"from"`
	To   string   `json:"to"`
	Kbps int      `json:"kbps"`
}

// Subtitles controls text-track retention.
type Subtitles struct {
	KeepLanguages      []string `json:"keep_languages,omitempty"`
	DropCommentary     bool     `json:"drop_commentary"`
	DropClosedCaptions bool     `json:"drop_closed_captions"`
	KeepForced         bool     `json:"keep_forced"`
}

// Rule is one ordered decision. The first matching rule wins.
type Rule struct {
	When Condition `json:"when"`
	Do   string    `json:"do"`
	Code string    `json:"code"`
}

// Condition is a conjunction: every populated field must hold.
type Condition struct {
	HeightLT   *int   `json:"height_lt,omitempty"`
	HeightGTE  *int   `json:"height_gte,omitempty"`
	Codec      string `json:"codec,omitempty"`
	BitrateLT  *int   `json:"bitrate_kbps_lt,omitempty"`
	BitrateGTE *int   `json:"bitrate_kbps_gte,omitempty"`
	// AudioCodecIn matches when any audio track uses one of these codecs. It is
	// how the rule list says "this file needs its audio fixed" without naming
	// a video property it does not care about.
	AudioCodecIn []string `json:"audio_codec_in,omitempty"`
	// AudioTracksGT matches when the file has more than this many audio
	// streams — the point where the track policy starts dropping some.
	AudioTracksGT *int `json:"audio_tracks_gt,omitempty"`
	Default       bool `json:"default,omitempty"`
}

// Safety holds the invariants that keep a bad decision from costing data.
type Safety struct {
	MinFreeGB          int     `json:"min_free_gb"`
	SizeRatioMin       float64 `json:"size_ratio_min"`
	SizeRatioMax       float64 `json:"size_ratio_max"`
	DurationTolSec     int     `json:"duration_tolerance_s"`
	KeepQuarantineDays int     `json:"keep_quarantine_days"`
	VerifySampleSec    int     `json:"verify_sample_seconds"`
	VerifyVMAF         bool    `json:"verify_vmaf"`
	MaxAttempts        int     `json:"max_attempts"`
	// Quarantine is "copy" (the default, and what an absent key means) or
	// "off". Off skips the copy that preserves the original: the swap is still
	// a single rename, but the bytes that were replaced are gone. It exists
	// because the copy costs a full read and write of every file, which on a
	// library this size is the largest consumer of the fast volume.
	Quarantine string `json:"quarantine,omitempty"`
}

// QuarantineEnabled reports whether the original is copied before the swap.
// An absent value means copy: losing the safety net has to be explicit, and a
// Go zero value must never be able to turn it off by accident.
func (s Safety) QuarantineEnabled() bool { return s.Quarantine != "off" }

// Notify calls out to the *arr stack after a successful replacement.
type Notify struct {
	Radarr          string   `json:"radarr,omitempty"`
	Sonarr          string   `json:"sonarr,omitempty"`
	On              []string `json:"on,omitempty"`
	JellyfinRefresh bool     `json:"jellyfin_refresh"`
}

// Server configures the HTTP surface. The node authenticates with the token;
// without one the server refuses to start rather than serving an open
// endpoint that can claim jobs.
type Server struct {
	Listen string `json:"listen"`
	Token  string `json:"token,omitempty"`
	// URL is where the node finds the orchestrator. The server ignores it; it
	// exists so both roles read one config file instead of two.
	URL string `json:"url,omitempty"`
}

// Schema returns the embedded JSON Schema.
func Schema() []byte { return schemaJSON }

var (
	schemaOnce sync.Once
	compiled   *jsonschema.Schema
	compileErr error
)

func compiledSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
		if err != nil {
			compileErr = fmt.Errorf("embedded schema is not valid JSON: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("config.v1.json", doc); err != nil {
			compileErr = fmt.Errorf("add schema resource: %w", err)
			return
		}
		compiled, compileErr = c.Compile("config.v1.json")
	})
	return compiled, compileErr
}

// Load reads, validates and defaults the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Decode validates raw JSON against the schema, then against Check.
func Decode(data []byte) (*Config, error) {
	sch, err := compiledSchema()
	if err != nil {
		return nil, err
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := sch.Validate(instance); err != nil {
		return nil, schemaError(err)
	}

	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := dec.Decode(new(struct{})); err != io.EOF {
		return nil, fmt.Errorf("decode: trailing data after the top-level object")
	}

	c.Schedule.migrate()
	c.applyDefaults()
	if err := c.Check(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Workers.Encode == 0 {
		c.Workers.Encode = 1
	}
	if c.Workers.Probe == 0 {
		c.Workers.Probe = 1
	}
	if c.Quality.ProbeOutliersAbove == 0 {
		c.Quality.ProbeOutliersAbove = 12000
	}
	if c.Audio.MaxTracks == 0 {
		c.Audio.MaxTracks = 3
	}
	if len(c.Notify.On) == 0 {
		c.Notify.On = []string{"replaced", "failed"}
	}
	if c.Server.Listen == "" {
		c.Server.Listen = ":8099"
	}
	// VerifyVMAF is a bool: false is a legitimate explicit choice and is left
	// alone. The shipped example sets it; a config that omits it gets false.
}
