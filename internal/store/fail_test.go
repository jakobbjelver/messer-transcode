package store

import (
	"context"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// claimOne seeds a single claimable row and hands it out.
func claimOne(t *testing.T, s *Store, path string) int64 {
	t.Helper()
	ctx := context.Background()
	if _, _, err := s.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: path, Size: 4_000_000_000, MtimeNS: 1},
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.Queue(ctx, QueueFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, r := range page.Rows {
		if r.Path == path {
			id = r.ID
		}
	}
	if id == 0 {
		t.Fatalf("seeded row %s not found", path)
	}
	probe := media.Probe{
		Container: "mkv", VideoCodec: "h264", Width: 1920, Height: 1080,
		DurationS: 3600, BitrateKbps: 8000, SizeBytes: 4_000_000_000,
		Audio: []media.AudioTrack{{Lang: "eng", Codec: "eac3", Channels: 6}},
	}
	if err := s.SetProbe(ctx, id, probe, "probe", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDecision(ctx, id, Decision{
		Decision: "encode", Code: "R_H264_ENCODE", Reason: "8000 kbps H.264",
		KeyKbps: 6000, State: "planned",
	}); err != nil {
		t.Fatal(err)
	}
	work, err := s.ClaimWork(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 1 || work[0].ID != id {
		t.Fatalf("claimed %v, want row %d", work, id)
	}
	return id
}

// A structural verification failure cannot improve on a retry: the same source,
// the same encoder and the same rung produce the same file. Retrying it cost
// three to four full encodes per file before the row parked — City Lights on
// 2026-11-15 failed the same 0.324 ratio four times — so it parks at once.
func TestStructuralVerifyFailureParksAtOnce(t *testing.T) {
	ctx := context.Background()
	for _, code := range []string{"E_VERIFY_SIZE_RATIO", "E_VERIFY_DURATION"} {
		s := tempStore(t)
		id := claimOne(t, s, "/media/Structural/Film (2020) [1080p].mkv")

		state, err := s.Fail(ctx, id, code, "the same output, whatever the attempt", 3)
		if err != nil {
			t.Fatal(err)
		}
		if state != "failed" {
			t.Errorf("%s: state = %q after one attempt, want failed", code, state)
		}
	}
}

// A transient failure still gets its retries: an ffmpeg crash or a busy disk is
// allowed to recover.
func TestTransientFailureKeepsRetrying(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	id := claimOne(t, s, "/media/Transient/Film (2020) [1080p].mkv")

	for attempt := 1; attempt <= 2; attempt++ {
		state, err := s.Fail(ctx, id, "E_ENCODE_FFMPEG", "ffmpeg exited 1", 3)
		if err != nil {
			t.Fatal(err)
		}
		if state != "planned" {
			t.Fatalf("attempt %d: state = %q, want planned", attempt, state)
		}
	}
	state, err := s.Fail(ctx, id, "E_ENCODE_FFMPEG", "ffmpeg exited 1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Errorf("third attempt: state = %q, want failed", state)
	}
}
