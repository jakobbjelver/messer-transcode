package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/configdiff"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// problemsOf turns a config error into the list the dialog renders. A schema
// violation carries every leaf; anything else is one problem at the root, so
// the caller never has to parse a message.
func problemsOf(err error) []config.Problem {
	if err == nil {
		return []config.Problem{}
	}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		return ve.Problems
	}
	return []config.Problem{{Path: "/", Message: err.Error()}}
}

// config is the configuration every handler reads. It is never nil.
func (s *Server) config() *config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// install swaps the configuration in one step and remembers what it hashed to.
func (s *Server) install(c *config.Config, sha string) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfg = c
	s.cfgSHA = sha
}

// diskState is what the watcher last saw on the volume.
func (s *Server) diskState() (sha, errMsg string) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.diskSHA, s.diskErr
}

func (s *Server) setDisk(sha, errMsg string) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.diskSHA = sha
	s.diskErr = errMsg
}

// keepToken carries the node token across a reload.
//
// The token is not in the config file: it is read once from a secret at
// startup. A config that arrives over HTTP, or is re-read from the volume, has
// an empty one — installing it verbatim would silently switch node
// authentication off, which is the one failure here that is a security bug
// rather than a bug.
func (s *Server) keepToken(next *config.Config) {
	if next.Server.Token == "" {
		next.Server.Token = s.config().Server.Token
	}
}

// WatchConfig keeps the running configuration in step with the file.
//
// A hand edit on the volume is a normal way to change this system (it is how
// every change was made before this release), so the server adopts it rather
// than waiting for a restart. The revision ledger records it as dirty, and the
// UI offers to adopt it — history is for changes a person makes, and a watcher
// is not a person.
func (s *Server) WatchConfig(ctx context.Context) {
	s.pollConfig()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollConfig()
		}
	}
}

func (s *Server) pollConfig() {
	if s.cfgPath == "" {
		return
	}
	raw, err := os.ReadFile(s.cfgPath)
	if err != nil {
		s.setDisk("", err.Error())
		return
	}
	sha, err := config.Fingerprint(raw)
	if err != nil {
		s.setDisk("", err.Error())
		return
	}
	s.setDisk(sha, "")

	s.cfgMu.RLock()
	running := s.cfgSHA
	s.cfgMu.RUnlock()
	if sha == running {
		return
	}

	next, err := config.Decode(raw)
	if err != nil {
		s.setDisk(sha, err.Error())
		return
	}
	s.keepToken(next)
	s.install(next, sha)
	s.log.Info("config reloaded from disk", "sha", sha[:12])
}

// ── state, as the config screen sees it ───────────────────────────────────

type configState struct {
	// HeadRev is the newest stored revision, 0 when nothing has been saved.
	HeadRev int64  `json:"head_rev"`
	HeadSHA string `json:"head_sha"`
	// Dirty means the file on the volume is not the head revision: edited by
	// hand, or saved to a volume this instance cannot read.
	Dirty string `json:"dirty"` // "" | "edited" | "absent" | "invalid"
	// DiskSHA is the fingerprint of the file the server can see, "" if none.
	DiskSHA string `json:"disk_sha"`
	// DiskError explains dirty=invalid.
	DiskError string `json:"disk_error,omitempty"`
	Applies   string `json:"applies"`
	// RunningSHA is the fingerprint of the configuration actually in memory.
	RunningSHA string `json:"running_sha"`
}

func (s *Server) configState(ctx context.Context) configState {
	var out configState
	head, err := s.st.HeadConfigRevision(ctx)
	if err == nil {
		out.HeadRev = head.Rev
		// Compare meanings, not storage forms: a revision recorded before this
		// build changed its renderer must not look like a hand edit.
		out.HeadSHA = canonicalSHA([]byte(head.Content))
	}
	diskSHA, diskErr := s.diskState()
	out.DiskSHA = diskSHA
	out.DiskError = diskErr
	s.cfgMu.RLock()
	out.RunningSHA = s.cfgSHA
	s.cfgMu.RUnlock()

	switch {
	case out.HeadRev == 0 && diskSHA == "":
		out.Dirty = "absent"
	case diskErr != "":
		out.Dirty = "invalid"
	case diskSHA != "" && diskSHA != out.HeadSHA:
		out.Dirty = "edited"
	case out.HeadRev == 0:
		out.Dirty = "edited"
	}
	// Where the running process stands relative to the newest revision. A hand
	// edit is running before it is recorded, so saying "next claim" there would
	// be wrong: nothing is waiting for anything.
	switch {
	case out.RunningSHA != "" && out.RunningSHA == out.HeadSHA:
		out.Applies = "live"
	case out.Dirty == "edited":
		out.Applies = "live, ahead of the newest revision"
	default:
		out.Applies = "next claim"
	}
	return out
}

// ── reads ─────────────────────────────────────────────────────────────────

// apiConfigHistory is the revision list: what changed, when, by whom.
func (s *Server) apiConfigHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	revs, err := s.st.ConfigRevisions(r.Context(), limit)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	if revs == nil {
		revs = []store.Revision{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"revisions": revs,
		"state":     s.configState(r.Context()),
	})
}

// apiConfigRevision returns one revision's document.
func (s *Server) apiConfigRevision(w http.ResponseWriter, r *http.Request) {
	rev, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
	if err != nil || rev <= 0 {
		s.fail(w, http.StatusBadRequest, "E_BAD_REV", "not a revision")
		return
	}
	got, err := s.st.ConfigRevision(r.Context(), rev)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, http.StatusNotFound, "E_NOT_FOUND", "no such revision")
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// apiConfigDiff compares two revisions (or the head against the disk).
func (s *Server) apiConfigDiff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)

	before, err := s.revisionContent(ctx, from)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_REV", err.Error())
		return
	}
	after, err := s.revisionContent(ctx, to)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_REV", err.Error())
		return
	}
	d, err := configdiff.Compute(canonicalForDiff(before), canonicalForDiff(after))
	if err != nil {
		s.fail(w, http.StatusUnprocessableEntity, "E_INVALID_CONFIG", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// canonicalSHA is the identity of a document as this build understands it.
func canonicalSHA(doc []byte) string {
	return config.Hash(canonicalForDiff(doc))
}

// canonicalForDiff puts a document into the form this build would store before
// it is compared.
//
// Without this, the first save after the schedule model changed would report
// every existing config as "weekday and weekend removed, windows added" — true
// of the bytes and false of the schedule. A diff is about what the
// configuration means, and the meaning of a legacy document is its migrated
// form. A document that does not decode is compared as written, which is the
// only thing left to do with it.
func canonicalForDiff(doc []byte) []byte {
	if len(doc) == 0 {
		return doc
	}
	c, err := config.Decode(doc)
	if err != nil {
		return doc
	}
	out, err := config.Render(c)
	if err != nil {
		return doc
	}
	return out
}

// revisionContent is a revision's bytes; 0 means "whatever is on disk now".
func (s *Server) revisionContent(ctx context.Context, rev int64) ([]byte, error) {
	if rev == 0 {
		raw, err := os.ReadFile(s.cfgPath)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	got, err := s.st.ConfigRevision(ctx, rev)
	if err != nil {
		return nil, err
	}
	return []byte(got.Content), nil
}

// ── dry run ───────────────────────────────────────────────────────────────

// apiConfigDryRun answers "what would this do?" without touching anything.
// It is the same validation the write path runs, so a draft that passes here
// cannot be rejected later for a reason it did not mention.
func (s *Server) apiConfigDryRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BODY", "could not read the body")
		return
	}
	var req struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BODY", err.Error())
		return
	}

	head, _ := s.st.HeadConfigRevision(ctx)
	before := canonicalForDiff([]byte(head.Content))

	parsed, perr := config.Decode(req.Config)
	var rendered []byte
	var sha string
	ok := perr == nil
	if ok {
		rendered, err = config.Render(parsed)
		if err != nil {
			ok = false
			perr = err
		} else {
			sha = config.Hash(rendered)
		}
	}

	out := map[string]any{
		"ok":       ok,
		"problems": problemsOf(perr),
		"head_rev": head.Rev,
		"head_sha": head.SHA,
		"sha":      sha,
		"state":    s.configState(ctx),
	}
	// A diff is still useful when the draft is invalid (it is usually valid JSON
	// with one bad number), so it is attempted either way.
	target := rendered
	if target == nil {
		target = canonicalForDiff(req.Config)
	}
	if d, derr := configdiff.Compute(before, target); derr == nil {
		out["diff"] = d
	}
	// The projection travels with the review: the cron lines are what the
	// schedule means to everything outside this app, and they belong next to
	// the diff that is about to change them.
	if parsed != nil {
		out["schedule"] = map[string]any{
			"cron":   parsed.Schedule.CronProjects(),
			"drains": parsed.Schedule.DrainBeforeMinutes,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── writes ────────────────────────────────────────────────────────────────

// apiConfigSave writes a new revision: validate, compare, write the file,
// record the revision, install it.
//
// base_rev is what makes concurrent editing safe. A tab that started from
// revision 7 and saves after someone else made revision 8 is told so, with the
// diff it missed — never silently overwritten.
func (s *Server) apiConfigSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Config  json.RawMessage `json:"config"`
		BaseRev int64           `json:"base_rev"`
		Message string          `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BODY", err.Error())
		return
	}
	if len(req.Config) == 0 {
		s.fail(w, http.StatusBadRequest, "E_BODY", "no config")
		return
	}

	head, err := s.st.HeadConfigRevision(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	if head.Rev != req.BaseRev {
		d, _ := configdiff.Compute(canonicalForDiff([]byte(head.Content)), canonicalForDiff(req.Config))
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{
				"code":    "E_CONFLICT",
				"message": "someone saved a newer revision while you were editing",
			},
			"head_rev": head.Rev,
			"head_sha": head.SHA,
			"diff":     d,
		})
		return
	}

	saved, rendered, problems, unchanged, err := s.commitConfig(ctx, req.Config, req.Message, "ui")
	if err != nil {
		if problems != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":    map[string]string{"code": "E_INVALID_CONFIG", "message": "the configuration breaks a rule"},
				"problems": problems,
			})
			return
		}
		s.fail(w, http.StatusInternalServerError, "E_WRITE", err.Error())
		return
	}
	if unchanged {
		// Saving the document that is already head is not an event: no new
		// revision and no file write. A history with rows that change nothing is
		// a history nobody reads.
		writeJSON(w, http.StatusOK, map[string]any{
			"rev":       saved.Rev,
			"sha":       saved.SHA,
			"config":    s.redacted(),
			"warnings":  []string{"nothing changed: this is already revision " + strconv.FormatInt(saved.Rev, 10)},
			"unchanged": true,
			"state":     s.configState(ctx),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rev":      saved.Rev,
		"sha":      saved.SHA,
		"config":   s.redacted(),
		"warnings": s.configWarnings(rendered),
		"state":    s.configState(ctx),
	})
}

// commitConfig is the one path that writes the file and records the revision.
// Save, adopt and revert all go through it, so they cannot drift apart.
func (s *Server) commitConfig(ctx context.Context, doc []byte, message, actor string) (store.Revision, *config.Config, []config.Problem, bool, error) {
	parsed, perr := config.Decode(doc)
	if perr != nil {
		return store.Revision{}, nil, problemsOf(perr), false, perr
	}
	rendered, err := config.Render(parsed)
	if err != nil {
		return store.Revision{}, nil, nil, false, err
	}
	sha := config.Hash(rendered)

	// Already head: no write, no revision. This is the guard that keeps an
	// unchanged draft from filling the history with rows that say nothing.
	if head, herr := s.st.HeadConfigRevision(ctx); herr == nil && canonicalSHA([]byte(head.Content)) == sha {
		return head, parsed, nil, true, nil
	}

	if strings.TrimSpace(message) == "" {
		message = "updated from the configuration screen"
	}
	if err := writeFileAtomic(s.cfgPath, rendered); err != nil {
		return store.Revision{}, nil, nil, false, err
	}

	rev, err := s.st.PutConfigRevision(ctx, store.Revision{
		SHA:       sha,
		CreatedAt: time.Now().Unix(),
		Actor:     actor,
		Message:   message,
		Content:   string(rendered),
	})
	if err != nil {
		return store.Revision{}, nil, nil, false, err
	}

	s.keepToken(parsed)
	s.install(parsed, sha)
	s.setDisk(sha, "")
	_ = s.st.Event(ctx, "info", "M_CONFIG_SAVED", 0,
		"configuration saved: "+message, `{"rev":`+strconv.FormatInt(rev.Rev, 10)+`}`)
	s.log.Info("config saved", "rev", rev.Rev, "sha", sha[:12], "actor", actor)
	return rev, parsed, nil, false, nil
}

// apiConfigAdopt records what is on the volume as a revision. It exists for the
// honest case: someone edited the file by hand, the server adopted it, and the
// history should say when.
func (s *Server) apiConfigAdopt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)

	raw, err := os.ReadFile(s.cfgPath)
	if err != nil {
		s.fail(w, http.StatusPreconditionFailed, "E_NO_CONFIG", "the config file cannot be read")
		return
	}
	if req.Message == "" {
		req.Message = "adopted the configuration edited on disk"
	}
	rev, _, problems, unchanged, err := s.commitConfig(ctx, raw, req.Message, "disk")
	if err != nil {
		if problems != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":    map[string]string{"code": "E_INVALID_CONFIG", "message": "the file on disk breaks a rule"},
				"problems": problems,
			})
			return
		}
		s.fail(w, http.StatusInternalServerError, "E_WRITE", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rev": rev.Rev, "sha": rev.SHA, "unchanged": unchanged, "state": s.configState(ctx)})
}

// apiConfigRevert restores a revision as a new one. History is append-only: a
// revert is a change like any other, not a deletion of what came after it.
func (s *Server) apiConfigRevert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rev, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
	if err != nil || rev <= 0 {
		s.fail(w, http.StatusBadRequest, "E_BAD_REV", "not a revision")
		return
	}
	target, err := s.st.ConfigRevision(ctx, rev)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, http.StatusNotFound, "E_NOT_FOUND", "no such revision")
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	msg := req.Message
	if msg == "" {
		msg = "restored revision " + strconv.FormatInt(rev, 10)
	}

	saved, _, problems, unchanged, err := s.commitConfig(ctx, []byte(target.Content), msg, "ui")
	if err != nil {
		if problems != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":    map[string]string{"code": "E_INVALID_CONFIG", "message": "that revision no longer validates"},
				"problems": problems,
			})
			return
		}
		s.fail(w, http.StatusInternalServerError, "E_WRITE", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rev": saved.Rev, "sha": saved.SHA, "unchanged": unchanged, "state": s.configState(ctx)})
}

// SeedConfigRevision records the configuration that is on disk as the first
// revision, once. Without it the first save would have nothing to diff against
// and the history would begin mid-story.
func (s *Server) SeedConfigRevision(ctx context.Context) error {
	if _, err := s.st.HeadConfigRevision(ctx); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	raw, err := os.ReadFile(s.cfgPath)
	if err != nil {
		return err
	}
	parsed, err := config.Decode(raw)
	if err != nil {
		return err
	}
	rendered, err := config.Render(parsed)
	if err != nil {
		return err
	}
	_, err = s.st.PutConfigRevision(ctx, store.Revision{
		SHA:       config.Hash(rendered),
		CreatedAt: time.Now().Unix(),
		Actor:     "boot",
		Message:   "imported on first run",
		Content:   string(rendered),
	})
	return err
}

// redacted is the configuration with the node token removed: a config screen
// has no reason to see the one secret this process holds.
func (s *Server) redacted() *config.Config {
	c := *s.config()
	c.Server.Token = ""
	return &c
}

// configWarnings says which parts of a save take effect where. This is the
// honest answer to "did my change do anything?" — most of it is live within a
// second, and one field is not.
func (s *Server) configWarnings(next *config.Config) []string {
	var out []string
	cur := s.config()
	if next.Server.Listen != cur.Server.Listen {
		out = append(out, "server.listen takes effect at the next deploy: the container pins --listen")
	}
	if next.Server.Token != "" && next.Server.Token != cur.Server.Token {
		out = append(out, "server.token is read from the token file, not the config")
	}
	if len(out) == 0 {
		out = append(out, "live: the server applies it now, the node applies it on its next claim")
	}
	return out
}

// writeFileAtomic is how the config is written: a temp file in the same
// directory, then a rename. A crash mid-write leaves the old file intact and
// never a truncated one — and the watcher, which polls this path, never reads a
// partial document.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name) // no-op once renamed
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
