package server

import (
	"context"
	"encoding/json"
	"github.com/jakobbjelver/messer-transcode/internal/config"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// noon on a Monday, in the test config's UTC zone: inside 00:00-23:59 and far
// from both edges, so a wall-clock minute cannot flip an assertion.
var testNoon = time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func postJSON(t *testing.T, s *Server, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func nodeHeaders() map[string]string { return map[string]string{"X-Node-Token": testToken} }

// A pause is the operator's hand on the queue, and the node has to feel it —
// not just the dashboard. The reason rides the claim header the node already
// logs, so "why did nothing run" has an answer in the node's own log.
func TestPausedQueueRefusesClaims(t *testing.T) {
	s := newTestServer(t)

	if ok, reason := s.claimsAllowed(context.Background(), testNoon); !ok {
		t.Fatalf("the seeded window should be open, got %q", reason)
	}

	rec := postJSON(t, s, "/api/v1/control/pause", `{"paused":true,"reason":"disk work"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pause: status %d — %s", rec.Code, rec.Body.String())
	}

	claim := postJSON(t, s, "/api/v1/node/claim", `{"node":"test"}`, nodeHeaders())
	if claim.Code != http.StatusNoContent {
		t.Fatalf("claim while paused: status %d, want 204", claim.Code)
	}
	if got := claim.Header().Get("X-Claim-Status"); got != RefusedPaused {
		t.Fatalf("claim status %q, want %q", got, RefusedPaused)
	}
	if ok, reason := s.claimsAllowed(context.Background(), testNoon); ok || reason != RefusedPaused {
		t.Fatalf("claimsAllowed = %v %q, want false %q", ok, reason, RefusedPaused)
	}

	if rec := postJSON(t, s, "/api/v1/control/pause", `{"paused":false}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("resume: status %d", rec.Code)
	}
	if ok, reason := s.claimsAllowed(context.Background(), testNoon); !ok {
		t.Fatalf("claims refused after resume: %q", reason)
	}

	// The pause and the resume are both in the feed: a queue that moved without
	// the node must be explainable.
	events := s.st
	list, err := events.RecentEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, e := range list {
		codes = append(codes, e.Code)
	}
	if !strings.Contains(strings.Join(codes, ","), "M_PAUSE") || !strings.Contains(strings.Join(codes, ","), "M_RESUME") {
		t.Fatalf("activity feed has no pause/resume: %v", codes)
	}
}

// Forcing the window open is the answer to "it is 19:00 and I want it to run
// now". It must beat the schedule, and `auto` must hand it back.
func TestWindowOverrideBeatsTheSchedule(t *testing.T) {
	s := newTestServer(t)
	// A window that is shut at every hour of the day: 00:00-00:01.
	s.config().Schedule.Windows = []config.ScheduleWindow{
		{Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, Start: "00:00", End: "00:01"},
	}

	if ok, _ := s.claimsAllowed(context.Background(), testNoon); ok {
		t.Fatal("the shrunken window should be shut at noon")
	}

	rec := postJSON(t, s, "/api/v1/control/window", `{"action":"open","minutes":30}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("force open: status %d — %s", rec.Code, rec.Body.String())
	}
	if ok, reason := s.claimsAllowed(context.Background(), testNoon); !ok {
		t.Fatalf("override did not open the window: %q", reason)
	}

	// Closing it again refuses claims even if the schedule would allow them.
	postJSON(t, s, "/api/v1/control/window", `{"action":"auto"}`, nil)
	s.config().Schedule.Windows = []config.ScheduleWindow{
		{Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, Start: "00:00", End: "23:59"},
	}
	postJSON(t, s, "/api/v1/control/window", `{"action":"close"}`, nil)
	if ok, reason := s.claimsAllowed(context.Background(), testNoon); ok || reason != RefusedForced {
		t.Fatalf("forced shut = %v %q, want false %q", ok, reason, RefusedForced)
	}

	if rec := postJSON(t, s, "/api/v1/control/window", `{"action":"auto"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("auto: status %d", rec.Code)
	}
	if ok, reason := s.claimsAllowed(context.Background(), testNoon); !ok {
		t.Fatalf("schedule not back in charge: %q", reason)
	}
}

// An override with a deadline must stop being true on its own — expiry is
// evaluated, never swept, so nothing has to be running for it to end.
func TestExpiredOverrideIsIgnored(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Minute).Unix()
	if err := s.st.SetOverride(ctx, store.OverrideClose, past, "test"); err != nil {
		t.Fatal(err)
	}
	ctl, err := s.st.Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ctl.Override != "" {
		t.Fatalf("override %q survived its expiry", ctl.Override)
	}
	if ok, _ := s.claimsAllowed(ctx, testNoon); !ok {
		t.Fatal("an expired override still refused claims")
	}
}

// A mutation without a JSON content type is refused: a cross-site form post
// cannot set that header without a preflight.
func TestMutationRequiresJSON(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/control/pause", strings.NewReader("paused=true"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415", rec.Code)
	}
}

// Retry is for parked or skipped work. A replaced file is a fact, not a queue
// entry: retrying it would re-encode something the library already has.
func TestRetryRefusesAFinishedFile(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	id := fileIDByPath(t, s, "Film One")

	if err := s.st.Finish(ctx, id, 4_000_000_000, 2_000_000_000, 95, nil); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, s, "/api/v1/files/"+itoa(id)+"/retry", `{}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("retry of a done file: status %d — %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "E_STATE") {
		t.Fatalf("body carries no E_STATE: %s", rec.Body.String())
	}

	// A file retired by hand can be retried: it is parked, not finished.
	// (Film Two is still planned — the seeded "leave it alone" row.)
	other := fileIDByPath(t, s, "Film Two")
	if rec := postJSON(t, s, "/api/v1/files/"+itoa(other)+"/skip", `{}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("manual skip: status %d — %s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, s, "/api/v1/files/"+itoa(other)+"/retry", `{}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry of a skipped file: status %d — %s", rec.Code, rec.Body.String())
	}
	page, err := s.st.Queue(ctx, store.QueueFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Rows {
		if r.ID == other && r.State != "planned" {
			t.Fatalf("retried file is %q, want planned", r.State)
		}
	}
}

// Forty selected rows from a page that has gone stale: the batch applies what
// still makes sense and names the rest, instead of failing whole or lying.
func TestBulkActionReportsRefusals(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	planned := fileIDByPath(t, s, "Film One")
	done := fileIDByPath(t, s, "Film Two")
	if err := s.st.Finish(ctx, done, 900_000_000, 400_000_000, 93, nil); err != nil {
		t.Fatal(err)
	}
	missing := int64(999_999)

	rec := postJSON(t, s, "/api/v1/queue/act", `{"action":"priority","ids":[`+
		itoa(planned)+`,`+itoa(done)+`,`+itoa(missing)+`],"priority":100}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d — %s", rec.Code, rec.Body.String())
	}

	var res store.ActResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || len(res.Refused) != 2 {
		t.Fatalf("applied=%d refused=%d, want 1 and 2", res.Applied, len(res.Refused))
	}
	reasons := map[string]int{}
	for _, r := range res.Refused {
		reasons[r.Reason]++
	}
	if reasons["state"] != 1 || reasons["missing"] != 1 {
		t.Fatalf("refusal reasons = %v", reasons)
	}

	// The priority really is what the claim order reads.
	work, err := s.st.ClaimWork(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 1 || work[0].ID != planned {
		t.Fatalf("claim order ignored the priority: %+v", work)
	}
}
