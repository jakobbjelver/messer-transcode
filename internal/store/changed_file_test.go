package store

import (
	"context"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// A file that changed on disk is a different file: the verdict, the reason and
// the rung were all measured from bytes that are no longer there. Leaving them
// behind stranded the row in "probed" with a stale verdict the planner never
// revisits — 51 rows and 0.14 TB after the 2026-11-17 scan, two of them
// queued encodes.
func TestAChangedFileLosesItsVerdict(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	write := func(size int64) int64 {
		t.Helper()
		if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
			{Path: "/media/Film/Film (2020).mkv", Size: size, MtimeNS: size},
		}, size); err != nil {
			t.Fatal(err)
		}
		page, err := s.Queue(ctx, QueueFilter{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Rows {
			if r.Path == "/media/Film/Film (2020).mkv" {
				return r.ID
			}
		}
		t.Fatal("row not found")
		return 0
	}

	id := write(100)
	if err := s.SetProbe(ctx, id, media.Probe{
		Container: "mkv", VideoCodec: "h264", Width: 1920, Height: 1080,
		DurationS: 3600, BitrateKbps: 8000, SizeBytes: 100,
		Audio: []media.AudioTrack{{Lang: "eng", Codec: "eac3", Channels: 6}},
	}, "probe-1", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDecision(ctx, id, Decision{
		Decision: "encode", Code: "R_H264_ENCODE", Reason: "8000 kbps H.264",
		KeyKbps: 6000, State: "planned",
	}); err != nil {
		t.Fatal(err)
	}

	// The same path, a different file.
	write(200)

	var decision, code string
	var target int
	if err := s.DB().QueryRow(`SELECT COALESCE(decision,''), COALESCE(reason_code,''), COALESCE(target_kbps,0)
FROM files WHERE id=?`, id).Scan(&decision, &code, &target); err != nil {
		t.Fatal(err)
	}
	if decision != "" || code != "" || target != 0 {
		t.Errorf("changed file kept decision=%q code=%q target=%d, want all cleared", decision, code, target)
	}

	// And an unchanged scan leaves a verdict alone.
	if err := s.SetDecision(ctx, id, Decision{
		Decision: "encode", Code: "R_H264_ENCODE", Reason: "8000 kbps H.264",
		KeyKbps: 6000, State: "planned",
	}); err != nil {
		t.Fatal(err)
	}
	write(200)
	if err := s.DB().QueryRow(`SELECT COALESCE(decision,'') FROM files WHERE id=?`, id).Scan(&decision); err != nil {
		t.Fatal(err)
	}
	if decision != "encode" {
		t.Errorf("unchanged file lost its verdict: decision=%q", decision)
	}
}

// A row that is already stuck in "probed" with a verdict — the shape the old
// upsert left behind — must still be judged, or the fix only helps new changes.
func TestFilesNeedingDecisionIncludesAStuckProbedRow(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	if _, err := s.DB().Exec(`
INSERT INTO files (id, path, library, size_bytes, mtime_ns, state, decision, reason_code,
                   target_kbps, probe_key, width, height, video_codec, bitrate_kbps, updated_at)
VALUES (1, '/media/Stuck/Film (2020).mkv', 'movies', 4000000000, 1, 'probed', 'skip',
        'R_NO_RULE', 0, 'probe', 1920, 1080, 'h264', 8000, 1)`); err != nil {
		t.Fatal(err)
	}

	rows, err := s.FilesNeedingDecision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != 1 {
		t.Fatalf("FilesNeedingDecision = %v, want the stuck probed row", rows)
	}
}
