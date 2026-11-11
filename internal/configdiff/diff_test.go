package configdiff

import (
	"strings"
	"testing"
)

const beforeDoc = `{
  "version": 1,
  "schedule": {"weekday": "00:30-05:30", "weekend": "02:00-07:00", "drain_before_minutes": 15, "timezone": "Europe/Stockholm"},
  "rules": [{"when": {"codec": "h264"}, "do": "encode", "code": "R_1"}, {"when": {"default": true}, "do": "skip", "code": "R_2"}],
  "quality": {"mode": "ladder", "vmaf_target": 95, "vmaf_tolerance": 1, "probe_outliers_above_kbps": 12000,
    "table": [{"codec": "h264", "height_gte": 1080, "kbps": 6000}, {"codec": "h264", "height_gte": 720, "kbps": 2700}]}
}`

func mustCompute(t *testing.T, before, after string) Diff {
	t.Helper()
	d, err := Compute([]byte(before), []byte(after))
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	return d
}

func TestNoChange(t *testing.T) {
	// Reindented and reordered keys: the same config, and the dialog must say so.
	reformatted := strings.ReplaceAll(beforeDoc, "\n", "\n    ")
	if d := mustCompute(t, beforeDoc, reformatted); !d.Empty() {
		t.Fatalf("formatting was reported as a change: %+v", d.Changes)
	}
}

func TestChangedValue(t *testing.T) {
	after := strings.Replace(beforeDoc, `"kbps": 6000`, `"kbps": 6200`, 1)
	d := mustCompute(t, beforeDoc, after)
	if len(d.Changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(d.Changes), d.Changes)
	}
	c := d.Changes[0]
	if c.Kind != Changed || c.Before != float64(6000) || c.After != float64(6200) {
		t.Fatalf("change = %+v", c)
	}
	if c.Group != "quality" {
		t.Fatalf("group = %q, want quality", c.Group)
	}
	if !strings.Contains(strings.ToLower(c.Label), "quality") {
		t.Fatalf("label = %q, should name the section", c.Label)
	}
	if !strings.Contains(d.Text, "- ") || !strings.Contains(d.Text, "+ ") || !strings.Contains(d.Text, "6200") {
		t.Fatalf("raw diff does not carry the change:\n%s", d.Text)
	}
}

func TestReorderIsOneChange(t *testing.T) {
	after := strings.Replace(beforeDoc,
		`[{"when": {"codec": "h264"}, "do": "encode", "code": "R_1"}, {"when": {"default": true}, "do": "skip", "code": "R_2"}]`,
		`[{"when": {"default": true}, "do": "skip", "code": "R_2"}, {"when": {"codec": "h264"}, "do": "encode", "code": "R_1"}]`, 1)
	d := mustCompute(t, beforeDoc, after)
	if d.Summary.Reordered != 1 || d.Summary.Changed != 0 || len(d.Changes) != 1 {
		t.Fatalf("a reorder should be one change: %+v", d.Changes)
	}
	if d.Changes[0].Kind != Reordered || d.Changes[0].Path != "/rules" {
		t.Fatalf("change = %+v", d.Changes[0])
	}
}

func TestAddedAndRemoved(t *testing.T) {
	after := strings.Replace(beforeDoc, `, "timezone": "Europe/Stockholm"`, "", 1)
	d := mustCompute(t, beforeDoc, after)
	if d.Summary.Removed != 1 || d.Changes[0].Path != "/schedule/timezone" {
		t.Fatalf("removal not reported: %+v", d.Changes)
	}

	after2 := strings.Replace(beforeDoc, `"version": 1,`, `"version": 1, "extra": {"a": 1},`, 1)
	d2 := mustCompute(t, beforeDoc, after2)
	if d2.Summary.Added != 1 || len(d2.Changes) != 1 {
		t.Fatalf("addition not reported: %+v", d2.Changes)
	}
}

func TestAgainstAnEmptyRevision(t *testing.T) {
	d := mustCompute(t, "", beforeDoc)
	if d.Summary.Added == 0 {
		t.Fatalf("every key of a first save should be an addition: %+v", d.Summary)
	}
	if len(d.Groups) == 0 {
		t.Fatal("no groups for a first save")
	}
}
