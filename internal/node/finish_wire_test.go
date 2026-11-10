package node

import (
	"context"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// TestFinishCarriesTheOutputFacts is the wire half of the replay fix: the store
// can only stop re-queueing replaced files if the node tells it what the new
// file looks like.
func TestFinishCarriesTheOutputFacts(t *testing.T) {
	f := newFakeServer(t, nil)
	client := NewClient(f.srv.URL, "tok", "test-node")

	out := &media.OutputFacts{
		Probe:   media.Probe{VideoCodec: "hevc", Height: 720, BitrateKbps: 2700, SizeBytes: 1234},
		MtimeNS: 9_999,
	}
	if err := client.Finish(context.Background(), 7, 1000, 1234, 95, out); err != nil {
		t.Fatalf("finish: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.states) == 0 {
		t.Fatal("no state call reached the server")
	}
	body := f.states[len(f.states)-1]
	if body["state"] != "done" {
		t.Fatalf("state = %v, want done", body["state"])
	}
	probe, ok := body["probe"].(map[string]any)
	if !ok {
		t.Fatalf("finish sent no probe: %v", body)
	}
	if got := probe["size_bytes"]; got != float64(1234) {
		t.Fatalf("probe.size_bytes = %v, want 1234", got)
	}
	if got := probe["video_codec"]; got != "hevc" {
		t.Fatalf("probe.video_codec = %v, want hevc", got)
	}
	if got := body["mtime_ns"]; got != float64(9_999) {
		t.Fatalf("mtime_ns = %v, want 9999", got)
	}

}
