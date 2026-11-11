package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// example loads the shipped config, which must always be valid.
func example(t *testing.T) *Config {
	t.Helper()
	data, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	c, err := Decode(data)
	if err != nil {
		t.Fatalf("example config must validate, got: %v", err)
	}
	return c
}

func TestExampleConfigIsValid(t *testing.T) {
	c := example(t)
	if c.Version != 1 {
		t.Errorf("version = %d, want 1", c.Version)
	}
	if len(c.Libraries) != 1 || c.Libraries[0].Name != "media" {
		t.Fatalf("libraries = %+v", c.Libraries)
	}
	if c.Workers.Encode != 1 {
		t.Errorf("default encode workers = %d, want 1", c.Workers.Encode)
	}
	if c.Quality.VMAFTarget != 95 {
		t.Errorf("vmaf_target = %v, want 95", c.Quality.VMAFTarget)
	}
	if last := c.Rules[len(c.Rules)-1]; !last.When.Default {
		t.Errorf("last rule must be the default rule, got %+v", last)
	}
}

func TestDecodeRejectsUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), `"version": 1,`, `"version": 1, "typo_key": true,`, 1)

	_, err = Decode([]byte(broken))
	if err == nil {
		t.Fatal("expected an error for an unknown top-level key")
	}
	if !strings.Contains(err.Error(), "typo_key") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
	ve := AsValidation(err)
	if ve == nil {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Problems) == 0 {
		t.Error("expected at least one problem")
	}
}

func TestDecodeRejectsOutOfRange(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"vmaf above 100", `"vmaf_target": 95.0`, `"vmaf_target": 120.0`},
		{"bad window", `"start": "00:30", "end": "05:30"`, `"start": "25:00", "end": "05:30"`},
		{"bad day", `"days": ["sat", "sun"]`, `"days": ["caturday"]`},
		{"bad codec", `"codec": "h264", "height_gte": 2000`, `"codec": "vp9", "height_gte": 2000`},
		{"zero workers", `"encode": 1`, `"encode": 0`},
		{"root catalogue", `"root": "/Volumes/Media/Library"`, `"root": "/"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("../../config.example.json")
			if err != nil {
				t.Fatal(err)
			}
			broken := strings.Replace(string(data), tc.from, tc.to, 1)
			if broken == string(data) {
				t.Fatalf("fixture did not change: %q not found", tc.from)
			}
			if _, err := Decode([]byte(broken)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// TestCheckCatches what the schema cannot: cross-field contradictions.
func TestCheckCatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name:   "duplicate rule code",
			mutate: func(c *Config) { c.Rules[1].Code = c.Rules[0].Code },
			want:   "duplicate rule code",
		},
		{
			name:   "no trailing default rule",
			mutate: func(c *Config) { c.Rules = c.Rules[:len(c.Rules)-1] },
			want:   "last rule must be the default rule",
		},
		{
			name:   "default rule with extra conditions",
			mutate: func(c *Config) { c.Rules[len(c.Rules)-1].When.Codec = "hevc" },
			want:   "cannot carry other conditions",
		},
		{
			name:   "impossible height range",
			mutate: func(c *Config) { h := 500; c.Rules[0].When.HeightLT = &h; c.Rules[0].When.HeightGTE = &h },
			want:   "can never exceed",
		},
		{
			name:   "quarantine inside scratch",
			mutate: func(c *Config) { c.Paths.Quarantine = c.Paths.Scratch + "/q" },
			want:   "must not live inside scratch",
		},
		{
			name:   "scratch inside a library",
			mutate: func(c *Config) { c.Paths.Scratch = c.Libraries[0].Root + "/tmp" },
			want:   "library",
		},
		{
			name:   "table out of order",
			mutate: func(c *Config) { c.Quality.Table[1].HeightGTE = 2200 },
			want:   "most-specific-first",
		},
		{
			name:   "1080p uncovered",
			mutate: func(c *Config) { c.Quality.Table = c.Quality.Table[:1] },
			want:   "never cover 1080p",
		},
		{
			name: "encode rule with no table row",
			mutate: func(c *Config) {
				c.Rules = append(c.Rules[:len(c.Rules)-1],
					Rule{When: Condition{Codec: "av1"}, Do: "encode", Code: "R_AV1"},
					Rule{When: Condition{Default: true}, Do: "skip", Code: "R_NO_RULE"})
			},
			want: "has no av1 rows",
		},
		{
			name:   "window shorter than drain",
			mutate: func(c *Config) {
				// The migrated config has windows; a legacy field would be
				// ignored, which is what the migration is for.
				c.Schedule.Windows = []ScheduleWindow{{Days: []string{"mon"}, Start: "05:00", End: "05:10"}}
			},
			want:   "not longer than drain_before_minutes",
		},
		{
			name:   "unknown timezone",
			mutate: func(c *Config) { c.Schedule.Timezone = "Mars/Olympus" },
			want:   "unknown IANA timezone",
		},
		{
			name:   "size ratio inverted",
			mutate: func(c *Config) { c.Safety.SizeRatioMin = 0.99 },
			want:   "must be lower than size_ratio_max",
		},
		{
			name:   "audio source equals target",
			mutate: func(c *Config) { c.Audio.Transcode[0].From = []string{"eac3"} },
			want:   "would re-encode lossy audio",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := example(t)
			tc.mutate(c)
			err := c.Check()
			if err == nil {
				t.Fatal("expected a problem")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestCheckIsClean confirms no false positives on the shipped example.
func TestCheckIsClean(t *testing.T) {
	if err := example(t).Check(); err != nil {
		t.Fatalf("example config should be clean: %v", err)
	}
}

func TestParseWindow(t *testing.T) {
	for _, tc := range []struct {
		in      string
		start   int
		end     int
		dur     int
		wantErr bool
	}{
		{"00:30-05:30", 30, 330, 300, false},
		{"23:00-03:00", 1380, 180, 240, false},
		{"05:30-00:30", 330, 30, 1140, false},
		{"", 0, 0, 0, true},
		{"00:30", 0, 0, 0, true},
		{"24:00-05:00", 0, 0, 0, true},
		{"00:60-05:00", 0, 0, 0, true},
	} {
		w, err := ParseWindow(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q should fail", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if w.StartMin != tc.start || w.EndMin != tc.end || w.DurationMin() != tc.dur {
			t.Errorf("%q = {%d,%d} dur %d, want {%d,%d} dur %d",
				tc.in, w.StartMin, w.EndMin, w.DurationMin(), tc.start, tc.end, tc.dur)
		}
	}
}

func TestWindowContains(t *testing.T) {
	w, err := ParseWindow("00:30-05:30")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		clock string
		want  bool
	}{
		{"00:00", false}, {"00:30", true}, {"03:00", true},
		{"05:29", true}, {"05:30", false}, {"12:00", false},
	} {
		hh, mm := mustClock(t, tc.clock)
		at := time.Date(2026, 11, 6, hh, mm, 0, 0, time.UTC)
		if got := w.Contains(at); got != tc.want {
			t.Errorf("%s in 00:30-05:30 = %v, want %v", tc.clock, got, tc.want)
		}
	}

	wrapped, err := ParseWindow("23:00-03:00")
	if err != nil {
		t.Fatal(err)
	}
	hh, mm := mustClock(t, "01:00")
	if !wrapped.Contains(time.Date(2026, 11, 6, hh, mm, 0, 0, time.UTC)) {
		t.Error("01:00 should be inside a wrapping 23:00-03:00 window")
	}

	if got := w.MinutesUntilEnd(time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC)); got != 30 {
		t.Errorf("minutes until end = %d, want 30", got)
	}
	if got := w.MinutesUntilEnd(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)); got != -1 {
		t.Errorf("outside window should be -1, got %d", got)
	}
}

func mustClock(t *testing.T, s string) (int, int) {
	t.Helper()
	m, err := parseClock(s)
	if err != nil {
		t.Fatal(err)
	}
	return m / 60, m % 60
}

func TestSchemaCompiles(t *testing.T) {
	if _, err := compiledSchema(); err != nil {
		t.Fatalf("embedded schema does not compile: %v", err)
	}
	if len(Schema()) == 0 {
		t.Fatal("Schema() returned nothing")
	}
}

// Midnight is the end of the day, not the start of the next one.
func TestWindowEndingAtMidnight(t *testing.T) {
	w, err := ParseWindow("22:00-00:00")
	if err != nil {
		t.Fatal(err)
	}
	if w.DurationMin() != 120 {
		t.Fatalf("duration = %d minutes, want 120", w.DurationMin())
	}
	if w.EndMin != 24*60 {
		t.Fatalf("end = %d, want 1440", w.EndMin)
	}
	// The same day, two hours later.
	s := Schedule{Windows: []ScheduleWindow{{Days: []string{"mon"}, Start: "22:00", End: "00:00"}}, Timezone: "UTC"}
	occ, ok := s.OccurrenceAt(time.Date(2026, 11, 9, 23, 30, 0, 0, time.UTC))
	if !ok {
		t.Fatal("23:30 monday should be inside a 22:00-00:00 window")
	}
	if !occ.End.Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("end = %s, want tuesday 00:00", occ.End)
	}
	// 23:59 must still be a valid whole-day window.
	if _, err := ParseWindow("00:00-23:59"); err != nil {
		t.Fatalf("whole-day window broke: %v", err)
	}
}
