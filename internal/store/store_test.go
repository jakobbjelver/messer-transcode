package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-opening must not try to re-apply 0001 and must not lose data.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer func() { _ = s2.Close() }()

	var version int
	if err := s2.DB().QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}
}

func TestScanProbeDecideFlow(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	files := []media.Scanned{
		{Path: "/media/a.mkv", Size: 6_000_000_000, MtimeNS: 1},
		{Path: "/media/b.mkv", Size: 1_000_000_000, MtimeNS: 2},
	}
	if _, _, err := s.UpsertScanned(ctx, "media", files); err != nil {
		t.Fatal(err)
	}

	pending, err := s.FilesNeedingProbe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}

	// Probe the first file only.
	first := pending[0]
	probe := media.Probe{
		Container: "matroska", VideoCodec: "h264", Width: 1920, Height: 1080,
		DurationS: 6000, SizeBytes: 6_000_000_000, BitrateKbps: 8000,
		Audio: []media.AudioTrack{{Index: 1, Codec: "eac3", Lang: "eng", Channels: 6}},
	}
	tmdb := int64(615777)
	if err := s.SetProbe(ctx, first.ID, probe, ProbeKey(first.Size, first.Mtime), &tmdb); err != nil {
		t.Fatal(err)
	}

	undecided, err := s.FilesNeedingDecision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(undecided) != 1 {
		t.Fatalf("undecided = %d, want 1", len(undecided))
	}
	if undecided[0].ID != first.ID {
		t.Errorf("wrong file undecided: %d", undecided[0].ID)
	}
	if undecided[0].Height != 1080 || undecided[0].Codec != "h264" || undecided[0].BitrateKbps != 8000 {
		t.Errorf("probe facts did not round-trip: %+v", undecided[0])
	}

	if err := s.SetDecision(ctx, first.ID, Decision{
		Do: "encode", Decision: "encode", Code: "R_H264_ENCODE",
		Reason: "H.264 at 8000 kbps → HEVC 5500 kbps", KeyKbps: 5500, State: "planned",
	}); err != nil {
		t.Fatal(err)
	}

	buckets, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2 (decided encode + undecided rest)", len(buckets))
	}
	var found bool
	for _, b := range buckets {
		if b.Decision == "encode" && b.ReasonCode == "R_H264_ENCODE" {
			found = true
			if b.Files != 1 {
				t.Errorf("files = %d, want 1", b.Files)
			}
			// 5500 kbps over 6000 s ≈ 4.125 GB.
			if b.EstimateOut < 4_000_000_000 || b.EstimateOut > 4_300_000_000 {
				t.Errorf("estimate = %d bytes, want ≈4.1e9", b.EstimateOut)
			}
		}
	}
	if !found {
		t.Errorf("encode bucket missing from %+v", buckets)
	}

	states, err := s.StateCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if states["planned"] != 1 || states["new"] != 1 {
		t.Errorf("states = %v, want one planned and one new", states)
	}
}

// TestUpsertResetsAChangedFile is the guard against acting on stale facts: a
// file whose size moved must go back to needing a probe.
func TestUpsertResetsAChangedFile(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	original := []media.Scanned{{Path: "/media/a.mkv", Size: 100, MtimeNS: 1}}
	if _, _, err := s.UpsertScanned(ctx, "media", original); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.FilesNeedingProbe(ctx)
	if err := s.SetProbe(ctx, pending[0].ID, media.Probe{
		VideoCodec: "h264", Height: 1080, BitrateKbps: 8000, DurationS: 10,
	}, ProbeKey(100, 1), nil); err != nil {
		t.Fatal(err)
	}

	if n, _ := s.FilesUpToDate(ctx); n != 1 {
		t.Fatalf("up to date = %d, want 1", n)
	}

	// Same path, new size: the probe is no longer trustworthy.
	changed := []media.Scanned{{Path: "/media/a.mkv", Size: 200, MtimeNS: 2}}
	if _, _, err := s.UpsertScanned(ctx, "media", changed); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.FilesUpToDate(ctx); n != 0 {
		t.Fatalf("up to date after change = %d, want 0", n)
	}
	pending, _ = s.FilesNeedingProbe(ctx)
	if len(pending) != 1 {
		t.Fatalf("pending after change = %d, want 1", len(pending))
	}
	if pending[0].Size != 200 {
		t.Errorf("size = %d, want 200", pending[0].Size)
	}
}

func TestDecidedFiles(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	if _, _, err := s.UpsertScanned(ctx, "media", []media.Scanned{{Path: "/media/a.mkv", Size: 1, MtimeNS: 1}}); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.FilesNeedingProbe(ctx)
	if err := s.SetProbe(ctx, pending[0].ID, media.Probe{
		VideoCodec: "hevc", Height: 1080, BitrateKbps: 4000, DurationS: 10,
	}, "k", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDecision(ctx, pending[0].ID, Decision{
		Decision: "remux", Code: "R_HEVC_EFFICIENT", Reason: "already efficient", State: "planned",
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.DecidedFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Decision != "remux" || rows[0].ReasonCode != "R_HEVC_EFFICIENT" {
		t.Fatalf("rows = %+v", rows)
	}
}
