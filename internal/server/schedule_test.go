package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jakobbjelver/messer-transcode/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClaimsAllowed(t *testing.T) {
	s := newTestServer(t)
	s.config().Schedule = config.Schedule{
		Windows:            []config.ScheduleWindow{{Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, Start: "00:30", End: "05:30"}},
		DrainBeforeMinutes: 15,
		Timezone:           "UTC",
	}

	at := func(h, m int) time.Time { return time.Date(2026, 11, 6, h, m, 0, 0, time.UTC) } // a Friday

	cases := []struct {
		name   string
		when   time.Time
		ok     bool
		reason string
	}{
		{"before the window", at(0, 0), false, RefusedWindow},
		{"inside", at(2, 0), true, ""},
		{"inside but close to close", at(5, 0), true, ""},
		{"draining", at(5, 20), false, RefusedDraining},
		{"drain boundary is exclusive", at(5, 15), false, RefusedDraining},
		{"after close", at(6, 0), false, RefusedWindow},
	}
	for _, c := range cases {
		ok, reason := s.claimsAllowed(context.Background(), c.when)
		if ok != c.ok || reason != c.reason {
			t.Errorf("%s: allowed=%v reason=%q, want %v %q", c.name, ok, reason, c.ok, c.reason)
		}
	}
}

// A node that dies mid-job leaves the row in "encoding". The next claim must
// hand it back rather than waiting for a human.
func TestAbandonedJobIsRequeued(t *testing.T) {
	s := newTestServer(t)

	claim := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/node/claim", strings.NewReader(`{"node":"mini"}`))
		req.Header.Set("X-Node-Token", testToken)
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := claim(); code != http.StatusOK {
		t.Fatalf("first claim: %d", code)
	}
	if code := claim(); code != http.StatusNoContent {
		t.Fatalf("second claim should find nothing, got %d", code)
	}

	// Age the claim past the lease: a node that died sends neither progress nor
	// a heartbeat, so both stamps stop when the claim happens.
	if _, err := s.st.DB().Exec(
		`UPDATE files SET updated_at = ?, lease_at = ? WHERE state = 'encoding'`,
		time.Now().Add(-2*leaseDuration).Unix(), time.Now().Add(-2*leaseDuration).Unix()); err != nil {
		t.Fatal(err)
	}

	if code := claim(); code != http.StatusOK {
		t.Fatalf("claim after lease expiry: %d, want 200 (the abandoned job)", code)
	}
}

// A job that is being worked on must survive its lease. The node heartbeats
// every couple of seconds while it encodes, and a prefetched file can wait a
// whole encode before the encoder takes it — so if the lease only counted from
// the claim, every file longer than the lease came back as "abandoned" and was
// claimed (and encoded) a second time.
func TestHeartbeatKeepsTheLease(t *testing.T) {
	s := newTestServer(t)

	claim := func() (int, int64) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/node/claim", strings.NewReader(`{"node":"mini"}`))
		req.Header.Set("X-Node-Token", testToken)
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return rec.Code, 0
		}
		var body struct {
			Job struct {
				ID int64 `json:"id"`
			} `json:"job"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("claim body: %v", err)
		}
		return rec.Code, body.Job.ID
	}

	code, id := claim()
	if code != http.StatusOK || id == 0 {
		t.Fatalf("first claim: %d job %d, want 200 with an id", code, id)
	}

	// The node reports progress, as it does every two seconds while encoding.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/node/jobs/%d/heartbeat", id),
		strings.NewReader(`{"fraction":0.4,"speed":9.5}`))
	req.Header.Set("X-Node-Token", testToken)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("heartbeat: %d, want 204", rec.Code)
	}

	// Ten minutes on: the claim stamp is old, the heartbeat is not.
	old := time.Now().Add(-10 * time.Minute).Unix()
	if _, err := s.st.DB().Exec(`UPDATE files SET updated_at = ?, started_at = ? WHERE id = ?`, old, old, id); err != nil {
		t.Fatal(err)
	}

	if code, got := claim(); code != http.StatusNoContent {
		t.Fatalf("claim after 10 minutes with a live heartbeat: %d (job %d), want 204 — the lease was not renewed", code, got)
	}
}

func TestNodeStateRejectsUnknown(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/node/jobs/1/state",
		strings.NewReader(`{"state":"finished"}`))
	req.Header.Set("X-Node-Token", testToken)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "E_BAD_STATE") {
		t.Errorf("body %s, want E_BAD_STATE", rec.Body.String())
	}
}
