package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Render must be reversible: what this app writes has to load again.
//
// The whole revision feature rests on that. A rendered config that fails
// validation would make the first save write a file the server refuses on its
// next boot — with the old config still in memory and the disk marked invalid,
// which is exactly the kind of failure that looks like "the UI is broken" three
// days later.
func TestRenderRoundTrips(t *testing.T) {
	docs := map[string]string{
		"example": "../../config.example.json",
	}
	for name, path := range docs {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			c, err := Decode(raw)
			if err != nil {
				t.Fatalf("the shipped example does not validate: %v", err)
			}
			out, err := Render(c)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(out); err != nil {
				t.Fatalf("rendered config does not load again: %v\n%s", err, out)
			}
			again, err := Decode(out)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Render(again)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != string(second) {
				t.Fatal("render is not a fixed point: a second render differs")
			}
			if Hash(out) != Hash(second) {
				t.Fatal("fingerprint is not stable")
			}
		})
	}
}

// A token lives in a secret file, never in the config, so a config without one
// must still render and validate.
func TestRenderWithoutAToken(t *testing.T) {
	c, err := Decode([]byte(`{
	  "version": 1,
	  "libraries": [{"name":"movies","root":"/media","extensions":[".mkv"]}],
	  "schedule": {"weekday":"00:30-05:30","weekend":"02:00-07:00","drain_before_minutes":15,"timezone":"Europe/Stockholm"},
	  "workers": {"encode":1,"probe":2},
	  "paths": {"scratch":"/tmp/s","quarantine":"/tmp/q"},
	  "quality": {"mode":"table","vmaf_target":95,"vmaf_tolerance":1,"table":[{"codec":"h264","height_gte":1080,"kbps":6000}]},
	  "rules": [{"when":{"default":true},"do":"skip","code":"R_ALL"}],
	  "safety": {"min_free_gb":20,"size_ratio_min":0.5,"size_ratio_max":0.95,"duration_tolerance_s":2,"keep_quarantine_days":3,"verify_sample_seconds":60,"verify_vmaf":true,"max_attempts":3},
	  "server": {"listen":":8099"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(out); err != nil {
		t.Fatalf("a tokenless config does not round-trip: %v\n%s", err, out)
	}
}
