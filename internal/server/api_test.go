package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// fileIDByPath finds a seeded row's id without assuming insertion order.
func fileIDByPath(t *testing.T, s *Server, contains string) int64 {
	t.Helper()
	page, err := s.st.Queue(context.Background(), store.QueueFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Rows {
		if strings.Contains(r.Path, contains) {
			return r.ID
		}
	}
	t.Fatalf("no seeded file containing %q", contains)
	return 0
}

// The settings screen needs paths and a redacted config; the token is the one
// secret this process holds, so its absence is the assertion that matters.
func TestAPIConfigRedactsTheNodeToken(t *testing.T) {
	s := newTestServer(t)
	rec := get(t, s, "/api/v1/config")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, testToken) {
		t.Fatal("config response leaks the node token")
	}

	var body struct {
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
		ConfigPath   string         `json:"config_path"`
		DBSizeBytes  int64          `json:"db_size_bytes"`
		UptimeSecond int            `json:"uptime_seconds"`
		Config       map[string]any `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body.ConfigPath == "" {
		t.Error("config_path is empty")
	}
	if body.DBSizeBytes < 0 {
		t.Errorf("db_size_bytes = %d", body.DBSizeBytes)
	}
	server, _ := body.Config["server"].(map[string]any)
	if server == nil {
		t.Fatalf("config has no server block: %v", body.Config)
	}
	if token, ok := server["token"].(string); ok && token != "" {
		t.Errorf("server.token = %q, want empty", token)
	}
}

// A file detail carries the tier's expectation and the store's own readable
// figures, so the UI re-implements neither the ladder lookup nor the copy.
func TestAPIFileCarriesTierAndStoreHelpers(t *testing.T) {
	s := newTestServer(t)
	id := fileIDByPath(t, s, "Film One")
	rec := get(t, s, "/api/v1/files/"+strconv.FormatInt(id, 10))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	var body struct {
		File          store.FileDetail `json:"file"`
		ExpectedVMAF  float64          `json:"expected_vmaf"`
		NominalHeight int              `json:"nominal_height"`
		ReclaimBytes  int64            `json:"reclaim_bytes"`
		AudioList     string           `json:"audio_list"`
		SubList       string           `json:"sub_list"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body.File.ID != id {
		t.Errorf("file.id = %d, want %d", body.File.ID, id)
	}
	if body.NominalHeight != 1080 {
		t.Errorf("nominal_height = %d, want 1080 for a 1080p source", body.NominalHeight)
	}
	if body.ExpectedVMAF <= 0 {
		t.Errorf("expected_vmaf = %v, want the tier's number", body.ExpectedVMAF)
	}
	if body.SubList == "" {
		t.Error("sub_list is empty; it must say something even with no tracks")
	}
}

// The queue filter is the old /queue?decision= page's behaviour, moved onto the
// API where the SPA reads it.
func TestAPIFilesFiltersByDecision(t *testing.T) {
	s := newTestServer(t)
	rec := get(t, s, "/api/v1/files?decision=skip")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Film Two") {
		t.Fatalf("filtered queue is missing the skipped file: %s", body)
	}
	if strings.Contains(body, "Film One") {
		t.Fatalf("filtered queue leaked a file with another decision: %s", body)
	}
}

// The stream is the dashboard's channel: one typed event, not a string map of
// template ids. Reading one event proves both the framing and the type.
func TestStreamEmitsTypedPulses(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}

	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no event before the timeout: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var pulse streamPulse
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &pulse); err != nil {
			t.Fatalf("event is not the typed pulse: %v", err)
		}
		if pulse.Window.Spec == "" {
			t.Error("pulse carries no window spec")
		}
		if pulse.Dashboard == nil {
			t.Error("pulse carries no dashboard")
		}
		if pulse.At == 0 {
			t.Error("pulse carries no timestamp")
		}
		return
	}
}

// The pulse's revision is the whole cache-invalidation contract: a queue change
// must move it, and a quiet second must not. Without that, either every page
// polls or every page goes stale.
func TestPulseRevisionRisesWithEvents(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	before := s.pulse(ctx)
	if err := s.st.Event(ctx, "info", "T_REV", 0, "revision moved", ""); err != nil {
		t.Fatal(err)
	}
	after := s.pulse(ctx)
	if after.Rev <= before.Rev {
		t.Fatalf("rev did not move: %d then %d", before.Rev, after.Rev)
	}

	// A second pulse with no new event must report the same revision, or the
	// client would invalidate once a second and the cache would buy nothing.
	quiet := s.pulse(ctx)
	if quiet.Rev != after.Rev {
		t.Fatalf("rev moved without an event: %d then %d", after.Rev, quiet.Rev)
	}
}
