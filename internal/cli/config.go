package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/configdiff"
)

// cmdConfig is the configuration's non-UI path.
//
// The rule this release is built on is that the dashboard is a layer, never the
// only interface: every button has a verb, both go through one API. Which is
// also what makes these usable from a cron job or an ssh session at 02:00 —
// `config diff` before `config save` is the same review the dialog performs.
func cmdConfig(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	if len(args) == 0 {
		// A closed pipe is not a failure of the command; same rule as say().
		_, _ = fmt.Fprint(stderr, `usage: messer-transcode config <verb> [flags]

  show                     the running configuration, as JSON
  history [-n 20]          the revision list
  diff [-from N] [-to M]   what changed between two revisions (0 = the file on disk)
  check <file|->           what saving this file would change, and whether it is valid
  save <file|-> [-m why]   write a revision from a JSON file, or stdin
  adopt [-m why]           record the file on the volume as a revision
  revert <rev> [-m why]    restore a revision as a new one
`)
		return ExitUsage
	}

	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet("config "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	serverURL := fs.String("server", "", "orchestrator base URL (default: server.url in the config)")
	tokenFile := fs.String("token-file", "", "read the node token from this file")
	message := fs.String("m", "", "why this change (kept in the history)")
	limit := fs.Int("n", 20, "how many revisions to list")
	from := fs.Int64("from", 0, "revision to compare from (0 = the file on disk)")
	to := fs.Int64("to", 0, "revision to compare to (0 = the file on disk)")

	// Split flags from positionals before parsing: Go's flag package stops at
	// the first non-flag argument, so `config save draft.json -m why` would
	// otherwise never see -m. Same fix as the queue verb.
	valueFlags := map[string]bool{"config": true, "server": true, "token-file": true, "m": true, "n": true, "from": true, "to": true}
	var flagArgs, positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flagArgs = append(flagArgs, a)
		name := strings.TrimLeft(a, "-")
		if valueFlags[name] && i+1 < len(rest) {
			flagArgs = append(flagArgs, rest[i+1])
			i++
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return ExitUsage
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitRuntime
	}
	c := newAPIClient(cfg, *serverURL, *tokenFile)

	switch verb {
	case "show":
		var out struct {
			Config json.RawMessage `json:"config"`
			State  struct {
				HeadRev int64  `json:"head_rev"`
				Dirty   string `json:"dirty"`
				Applies string `json:"applies"`
			} `json:"state"`
		}
		if _, err := c.do("GET", "/api/v1/config", nil, &out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if jsonOut {
			say(stdout, "%s\n", out.Config)
			return ExitOK
		}
		pretty, _ := json.MarshalIndent(out.Config, "", "  ")
		say(stdout, "%s\n", pretty)
		say(stderr, "revision %d · %s · %s\n", out.State.HeadRev, out.State.Applies, dirtyWord(out.State.Dirty))
		return ExitOK

	case "history":
		var out struct {
			Revisions []struct {
				Rev       int64  `json:"rev"`
				SHA       string `json:"sha"`
				CreatedAt int64  `json:"created_at"`
				Actor     string `json:"actor"`
				Message   string `json:"message"`
			} `json:"revisions"`
			State struct {
				HeadRev int64  `json:"head_rev"`
				Dirty   string `json:"dirty"`
			} `json:"state"`
		}
		if _, err := c.do("GET", fmt.Sprintf("/api/v1/config/history?limit=%d", *limit), nil, &out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if jsonOut {
			b, _ := json.Marshal(out)
			say(stdout, "%s\n", b)
			return ExitOK
		}
		for _, r := range out.Revisions {
			say(stdout, "%4d  %s  %-6s  %s\n", r.Rev, time.Unix(r.CreatedAt, 0).Format("2006-01-02 15:04"), r.Actor, r.Message)
		}
		say(stderr, "head %d · %s\n", out.State.HeadRev, dirtyWord(out.State.Dirty))
		return ExitOK

	case "check":
		if len(positional) != 1 {
			say(stderr, "usage: messer-transcode config check <file|->\n")
			return ExitUsage
		}
		doc, err := readDoc(positional[0])
		if err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if _, err := config.Decode(doc); err != nil {
			// The server says the same thing; asking it first would only add a
			// round trip to a local answer.
			say(stderr, "%v\n", err)
			return ExitInvalid
		}
		var out struct {
			OK       bool             `json:"ok"`
			Problems []config.Problem `json:"problems"`
			Diff     *configdiff.Diff `json:"diff"`
		}
		if _, err := c.do("POST", "/api/v1/config/dry-run", map[string]any{"config": json.RawMessage(doc)}, &out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if jsonOut {
			b, _ := json.Marshal(out)
			say(stdout, "%s\n", b)
			return ExitOK
		}
		if !out.OK {
			for _, p := range out.Problems {
				say(stderr, "%s: %s\n", p.Path, p.Message)
			}
			return ExitInvalid
		}
		if out.Diff != nil {
			printDiff(stdout, *out.Diff)
		}
		return ExitOK

	case "diff":
		f, t := *from, *to
		if f == 0 && t == 0 {
			// Default: the head revision against the file on disk, which is the
			// question "what would I be adopting?".
			var head struct {
				State struct {
					HeadRev int64 `json:"head_rev"`
				} `json:"state"`
			}
			if _, err := c.do("GET", "/api/v1/config/history?limit=1", nil, &head); err != nil {
				say(stderr, "%v\n", err)
				return ExitRuntime
			}
			f = head.State.HeadRev
		}
		var d configdiff.Diff
		if _, err := c.do("GET", fmt.Sprintf("/api/v1/config/diff?from=%d&to=%d", f, t), nil, &d); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if jsonOut {
			b, _ := json.Marshal(d)
			say(stdout, "%s\n", b)
			return ExitOK
		}
		printDiff(stdout, d)
		return ExitOK

	case "save":
		if len(positional) != 1 {
			say(stderr, "usage: messer-transcode config save <file|-> [-m why]\n")
			return ExitUsage
		}
		doc, err := readDoc(positional[0])
		if err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		// Validate locally first: the same answer in a millisecond beats a round
		// trip to be told about a typo.
		if _, err := config.Decode(doc); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		var head struct {
			State struct {
				HeadRev int64 `json:"head_rev"`
			} `json:"state"`
		}
		if _, err := c.do("GET", "/api/v1/config/history?limit=1", nil, &head); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		body := map[string]any{"config": json.RawMessage(doc), "base_rev": head.State.HeadRev, "message": *message}
		var out struct {
			Rev      int64    `json:"rev"`
			SHA      string   `json:"sha"`
			Warnings []string `json:"warnings"`
		}
		if _, err := c.do("POST", "/api/v1/config", body, &out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if jsonOut {
			b, _ := json.Marshal(out)
			say(stdout, "%s\n", b)
			return ExitOK
		}
		say(stdout, "saved revision %d (%s)\n", out.Rev, shortSHA(out.SHA))
		for _, w := range out.Warnings {
			say(stderr, "  %s\n", w)
		}
		return ExitOK

	case "adopt":
		var out struct {
			Rev int64  `json:"rev"`
			SHA string `json:"sha"`
		}
		if _, err := c.do("POST", "/api/v1/config/adopt", map[string]any{"message": *message}, &out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if jsonOut {
			b, _ := json.Marshal(out)
			say(stdout, "%s\n", b)
			return ExitOK
		}
		say(stdout, "adopted the file on disk as revision %d (%s)\n", out.Rev, shortSHA(out.SHA))
		return ExitOK

	case "revert":
		if len(positional) != 1 {
			say(stderr, "usage: messer-transcode config revert <rev> [-m why]\n")
			return ExitUsage
		}
		rev, err := strconv.ParseInt(positional[0], 10, 64)
		if err != nil || rev <= 0 {
			say(stderr, "not a revision: %s\n", positional[0])
			return ExitUsage
		}
		var out struct {
			Rev int64  `json:"rev"`
			SHA string `json:"sha"`
		}
		if _, err := c.do("POST", fmt.Sprintf("/api/v1/config/revert/%d", rev), map[string]any{"message": *message}, &out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		if jsonOut {
			b, _ := json.Marshal(out)
			say(stdout, "%s\n", b)
			return ExitOK
		}
		say(stdout, "revision %d restored as revision %d\n", rev, out.Rev)
		return ExitOK
	}

	say(stderr, "unknown config verb %q\n", verb)
	return ExitUsage
}

func printDiff(w io.Writer, d configdiff.Diff) {
	if d.Empty() {
		say(w, "no changes\n")
		return
	}
	group := ""
	for _, c := range d.Changes {
		if c.Group != group {
			group = c.Group
			say(w, "\n%s\n", strings.ToUpper(group))
		}
		switch c.Kind {
		case configdiff.Added:
			say(w, "  + %-44s %v\n", c.Label, c.After)
		case configdiff.Removed:
			say(w, "  - %-44s %v\n", c.Label, c.Before)
		case configdiff.Reordered:
			say(w, "  ~ %s\n", c.Label)
		default:
			say(w, "  ~ %-44s %v → %v\n", c.Label, c.Before, c.After)
		}
	}
	s := d.Summary
	say(w, "\n%d changed, %d added, %d removed, %d reordered\n", s.Changed, s.Added, s.Removed, s.Reordered)
}

// readDoc takes a path, or "-" for stdin — the shape every CLI in this family
// uses, and the one that makes `config show | config save -` work.
func readDoc(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func dirtyWord(dirty string) string {
	switch dirty {
	case "":
		return "the file matches it"
	case "edited":
		return "the file on disk has been edited by hand"
	case "invalid":
		return "the file on disk does not load"
	default:
		return "no configuration file"
	}
}
