package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

const testToken = "0123456789abcdef-token"

func ptr[T any](v T) *T { return &v }

func testConfig() *config.Config {
	return &config.Config{
		Version:   1,
		Libraries: []config.Library{{Name: "movies", Root: "/media", Extensions: []string{".mkv"}}},
		Schedule: config.Schedule{
			Weekday: "00:00-23:59", Weekend: "00:00-23:59",
			DrainBeforeMinutes: 0, Timezone: "UTC",
		},
		Workers: config.Workers{Encode: 1, Probe: 2, IngestBuffer: 2},
		Paths:   config.Paths{Scratch: "/tmp/scratch", Quarantine: "/tmp/q"},
		Quality: config.Quality{
			VMAFTarget:    95,
			VMAFTolerance: 1,
			Table: []config.QualityRow{
				{Codec: "h264", HeightGTE: 1080, Kbps: 6000, ExpectedVMAF: ptr(95.0)},
				{Codec: "h264", HeightGTE: 720, Kbps: 2700, ExpectedVMAF: ptr(92.9)},
			},
		},
		Server: config.Server{Listen: ":8099", Token: testToken},
	}
}

// newTestServer seeds two files: one to encode, one to leave alone.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	scanned := []media.Scanned{
		{Path: "/media/Film One/Film One (2020) [1080p].mkv", Size: 4_000_000_000, MtimeNS: 1},
		{Path: "/media/Film Two/Film Two (2021) [720p].mkv", Size: 900_000_000, MtimeNS: 2},
	}
	if _, _, err := st.UpsertScanned(ctx, "movies", scanned); err != nil {
		t.Fatal(err)
	}
	page, err := st.Queue(ctx, store.QueueFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("seeded %d rows, want 2", len(page.Rows))
	}
	for _, r := range page.Rows {
		// A probe is what makes a row real: dimensions pick the ladder tier and the
		// audio/subtitle lists are what the detail screen shows. It must come before
		// the decision, because probing resets the row's state.
		probe := media.Probe{
			Container:   "mkv",
			VideoCodec:  "h264",
			Width:       1920,
			Height:      1080,
			DurationS:   3600,
			BitrateKbps: 8000,
			SizeBytes:   r.SizeBytes,
			Audio:       []media.AudioTrack{{Lang: "eng", Codec: "eac3", Channels: 6}},
		}
		if strings.Contains(r.Path, "Film Two") {
			probe.Width, probe.Height, probe.BitrateKbps = 1280, 720, 1500
		}
		if err := st.SetProbe(ctx, r.ID, probe, "probe-"+strconv.FormatInt(r.ID, 10), nil); err != nil {
			t.Fatal(err)
		}

		if strings.Contains(r.Path, "Film One") {
			err = st.SetDecision(ctx, r.ID, store.Decision{
				Decision: "encode", Code: "R_H264_ENCODE", Reason: "1080p H.264 at 8000 kbps",
				KeyKbps: 6000, State: "planned",
			})
		} else {
			err = st.SetDecision(ctx, r.ID, store.Decision{
				Decision: "skip", Code: "R_LOW_RES", Reason: "720p: below 1080p",
				KeyKbps: 0, State: "skipped",
			})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return New(testConfig(), st, "/tmp/config.json", "/tmp/state.db", nil)
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The SPA owns every non-API path. With a built frontend that is 200 and
// index.html; on a checkout where `make web` has not run it is 503 with one
// sentence. Either way it is never a JSON error and never a 404 — the routing
// contract is the server's, the content is web's (see web/web_test.go).
func TestAppRoutesReachTheSPA(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{"/", "/queue", "/queue?decision=skip", "/files/1", "/settings", "/runs"} {
		rec := get(t, s, p)
		if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 200 (built) or 503 (not built)", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: answered with the JSON error envelope", p)
		}
	}
}

// An unknown /api path must not fall through to index.html: a client parsing
// HTML as JSON reports the wrong fault.
func TestUnknownAPIPathIsJSON404(t *testing.T) {
	s := newTestServer(t)
	rec := get(t, s, "/api/v1/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q, want application/json", ct)
	}
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body.Error.Code != "E_NO_ROUTE" {
		t.Errorf("code %q, want E_NO_ROUTE", body.Error.Code)
	}
}

// A missing file must be a 404 with a machine-readable code, not a panic or an
// empty page: the agent path reads the code.
func TestUnknownFileIs404JSON(t *testing.T) {
	s := newTestServer(t)
	rec := get(t, s, "/api/v1/files/9999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body.Error.Code != "E_NOT_FOUND" {
		t.Errorf("code %q, want E_NOT_FOUND", body.Error.Code)
	}
}

func TestStatusAPI(t *testing.T) {
	s := newTestServer(t)
	rec := get(t, s, "/api/v1/status")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Dashboard struct {
			TotalFiles int            `json:"total_files"`
			Decisions  map[string]int `json:"decisions"`
		} `json:"dashboard"`
		Window struct {
			Spec string `json:"spec"`
			Open bool   `json:"open"`
		} `json:"window"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Dashboard.TotalFiles != 2 {
		t.Errorf("total_files = %d, want 2", body.Dashboard.TotalFiles)
	}
	if body.Dashboard.Decisions["encode"] != 1 {
		t.Errorf("encode = %d, want 1", body.Dashboard.Decisions["encode"])
	}
	if body.Window.Spec == "" {
		t.Error("window spec is empty")
	}
}

// The claim endpoint is the contract the node depends on: no token, no work.
func TestNodeClaim(t *testing.T) {
	s := newTestServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/node/claim", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated claim: status %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/node/claim", strings.NewReader(`{"node":"mini"}`))
	req.Header.Set("X-Node-Token", testToken)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: status %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Job struct {
			ID         int64  `json:"id"`
			Path       string `json:"path"`
			Decision   string `json:"decision"`
			TargetKbps int    `json:"target_kbps"`
		} `json:"job"`
		LeaseSeconds int `json:"lease_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Job.Decision != "encode" || body.Job.TargetKbps != 6000 {
		t.Errorf("job = %+v, want the encode decision at 6000 kbps", body.Job)
	}
	if body.LeaseSeconds == 0 {
		t.Error("lease_seconds missing")
	}

	// The same call again must find nothing: the first claim took the file.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/node/claim", strings.NewReader(`{"node":"mini"}`))
	req.Header.Set("X-Node-Token", testToken)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("second claim: status %d, want 204", rec.Code)
	}
}

func TestNodeHeartbeatAndResult(t *testing.T) {
	s := newTestServer(t)

	// Claim first: a heartbeat on a file nobody holds is not a live job.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/node/claim", strings.NewReader(`{"node":"mini"}`))
	req.Header.Set("X-Node-Token", testToken)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status %d", rec.Code)
	}
	var claimed struct {
		Job struct {
			ID int64 `json:"id"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatal(err)
	}
	id := claimed.Job.ID
	if id == 0 {
		t.Fatalf("claim returned no id: %s", rec.Body.String())
	}
	path := "/api/v1/node/jobs/" + strconv.FormatInt(id, 10)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, path+"/heartbeat",
		strings.NewReader(`{"fraction":0.42,"speed":8.5}`))
	req.Header.Set("X-Node-Token", testToken)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("heartbeat status %d", rec.Code)
	}

	live, ok, err := s.st.Live(context.Background())
	if err != nil || !ok {
		t.Fatalf("no live job after heartbeat: %v", err)
	}
	if live.FileID != id || live.Fraction < 0.41 || live.Fraction > 0.43 {
		t.Errorf("live = %+v, want file %d near 42%%", live, id)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, path+"/state",
		strings.NewReader(`{"state":"skipped","code":"R_QUALITY_FLOOR","detail":"grain"}`))
	req.Header.Set("X-Node-Token", testToken)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("state status %d: %s", rec.Code, rec.Body.String())
	}
	var applied struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.State != "skipped" {
		t.Errorf("applied state %q, want skipped", applied.State)
	}

	f, err := s.st.File(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if f.State != "skipped" || f.ReasonCode != "R_QUALITY_FLOOR" {
		t.Errorf("file state %q reason %q, want skipped/R_QUALITY_FLOOR", f.State, f.ReasonCode)
	}
}

func TestWithinWindow(t *testing.T) {
	start, end, err := parseWindow("00:30-05:30")
	if err != nil {
		t.Fatal(err)
	}
	at := func(h, m int) time.Time {
		return time.Date(2026, 11, 6, h, m, 0, 0, time.UTC)
	}
	cases := []struct {
		when time.Time
		want bool
	}{
		{at(0, 29), false},
		{at(0, 30), true},
		{at(3, 0), true},
		{at(5, 29), true},
		{at(5, 30), false},
		{at(23, 59), false},
	}
	for _, c := range cases {
		if got := withinWindow(c.when, start, end); got != c.want {
			t.Errorf("withinWindow(%s) = %v, want %v", c.when.Format("15:04"), got, c.want)
		}
	}

	// A window that crosses midnight is the case a naive comparison gets wrong.
	start, end, _ = parseWindow("22:00-06:00")
	if !withinWindow(at(23, 0), start, end) {
		t.Error("23:00 should be inside 22:00-06:00")
	}
	if withinWindow(at(12, 0), start, end) {
		t.Error("12:00 should be outside 22:00-06:00")
	}
}

// Reclaim falls back to the projected encode size when the file has not run.
func TestReclaimProjection(t *testing.T) {
	f := store.FileDetail{
		SummaryRow:  store.SummaryRow{State: "planned", Target: 6000, SizeBytes: 4_000_000_000},
		DurationS:   3600,
		BytesBefore: 0,
		BytesAfter:  0,
	}
	// One hour at 6 Mbps is 2.7 GB, so about 1.3 GB should come back.
	got := f.Reclaim()
	if got < 1_200_000_000 || got > 1_400_000_000 {
		t.Errorf("reclaim = %d, want about 1.3e9", got)
	}
}
