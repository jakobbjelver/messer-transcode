package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClaimsAllowed(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Schedule.Weekday = "00:30-05:30"
	s.cfg.Schedule.Weekend = "00:30-05:30"
	s.cfg.Schedule.DrainBeforeMinutes = 15
	s.cfg.Schedule.Timezone = "UTC"

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

	// Age the claim past the lease.
	if _, err := s.st.DB().Exec(
		`UPDATE files SET updated_at = ? WHERE state = 'encoding'`,
		time.Now().Add(-2*leaseDuration).Unix()); err != nil {
		t.Fatal(err)
	}

	if code := claim(); code != http.StatusOK {
		t.Fatalf("claim after lease expiry: %d, want 200 (the abandoned job)", code)
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
