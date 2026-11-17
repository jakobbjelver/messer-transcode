package plan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// ladderConfig is the shape of the live ladder, small enough to read: an h264
// file at 1080 lines is priced at 6,000 kbps, at 720 lines at 2,700.
func ladderConfig() *config.Config {
	ten := 10
	expected := 92.9
	row := func(height, kbps int) config.QualityRow {
		return config.QualityRow{Codec: "h264", HeightGTE: height, Kbps: kbps, ExpectedVMAF: &expected}
	}
	return &config.Config{
		Quality: config.Quality{
			VMAFTarget:    95,
			VMAFTolerance: 1,
			MinSavingPct:  &ten,
			Table: []config.QualityRow{
				row(2000, 15600), row(1000, 6000), row(700, 2700), row(0, 960),
			},
		},
		Rules: []config.Rule{
			{When: config.Condition{Codec: "h264"}, Do: "encode", Code: "R_H264_ENCODE"},
			{When: config.Condition{Default: true}, Do: "skip", Code: "R_NO_RULE"},
		},
	}
}

// The defect this exercises: Following (1999) is 1,436x1,080 4:3 at 10,472 kbps
// and was decided as a 720p file, so the queue held it at 2,700 kbps where it
// scored VMAF 25.5. The ladder now reads its height, but a verdict is only ever
// written once — Redecide is what carries the fix to rows already judged.
func TestRedecideReprisesANarrowFrame(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	ins := func(id int64, path, state string, target int) {
		t.Helper()
		if _, err := st.DB().Exec(`
INSERT INTO files (id, path, library, size_bytes, mtime_ns, state, decision, reason_code,
                   target_kbps, probe_key, width, height, video_codec, bitrate_kbps, audio_tracks, updated_at)
VALUES (?, ?, 'movies', 5626581200, 1, ?, 'encode', 'R_H264_ENCODE', ?, 'probe',
        1436, 1080, 'h264', 10472, '[{"lang":"eng","codec":"eac3","channels":6}]', 1)`,
			id, path, state, target); err != nil {
			t.Fatal(err)
		}
	}
	ins(1, "/media/Following (1999) [Bluray-1080p].mkv", "planned", 2700)
	ins(2, "/media/Placed (2020) [Bluray-1080p].mkv", "done", 2700)
	ins(3, "/media/Untouched (2021) [Bluray-1080p].mkv", "planned", 2700)

	n, err := Redecide(ctx, st, ladderConfig(), []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("re-decided %d rows, want 1 (the done row is settled)", n)
	}

	var target int
	var code string
	if err := st.DB().QueryRow(`SELECT target_kbps, reason_code FROM files WHERE id = 1`).
		Scan(&target, &code); err != nil {
		t.Fatal(err)
	}
	if target != 6000 {
		t.Errorf("target = %d kbps, want 6000 (a 1,080-line frame is a 1080p master)", target)
	}
	if code != "R_H264_ENCODE" {
		t.Errorf("reason = %q, want R_H264_ENCODE", code)
	}

	// The done row keeps its verdict and its state.
	var state string
	if err := st.DB().QueryRow(`SELECT state, target_kbps FROM files WHERE id = 2`).
		Scan(&state, &target); err != nil {
		t.Fatal(err)
	}
	if state != "done" || target != 2700 {
		t.Errorf("done row = %s at %d kbps, want done at 2700", state, target)
	}

	// A row outside the id list is untouched.
	if err := st.DB().QueryRow(`SELECT target_kbps FROM files WHERE id = 3`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if target != 2700 {
		t.Errorf("row 3 target = %d, want 2700 (not in the id list)", target)
	}
}
