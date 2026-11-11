package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

// apiClient is the CLI's hand on a running orchestrator.
//
// D10 says the UI is a layer, never the only interface: every button in the
// dashboard has a verb here, and they share one client so the two cannot drift
// into different endpoints.
type apiClient struct {
	base  string
	token string
}

func newAPIClient(cfg *config.Config, serverURL, tokenFile string) *apiClient {
	base := serverURL
	if base == "" {
		base = cfg.Server.URL
	}
	if base == "" {
		base = "http://127.0.0.1:8099"
	}
	token := cfg.Server.Token
	if tokenFile != "" {
		if b, err := os.ReadFile(tokenFile); err == nil {
			token = strings.TrimSpace(string(b))
		}
	}
	return &apiClient{base: strings.TrimRight(base, "/"), token: token}
}

func (c *apiClient) do(method, path string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("X-Node-Token", c.token)
	}
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 400 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error.Message != "" {
			return res.StatusCode, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return res.StatusCode, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, err
		}
	}
	return res.StatusCode, nil
}

// cmdQueue is the steering verb: pause the queue, force the window, or act on
// one file. It is deliberately chatty about what happened, because the whole
// point is that a human can see the effect without opening a browser.
func cmdQueue(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	if len(args) == 0 {
		say(stderr, "queue needs a verb: pause | resume | open | close | auto | retry <id> | skip <id> | priority <id> <-100|0|100>\n")
		return ExitUsage
	}
	verb := args[0]

	fs := flag.NewFlagSet("queue "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	serverURL := fs.String("server", "", "orchestrator base URL (default: server.url in the config)")
	tokenFile := fs.String("token-file", "", "read the node token from this file")
	forHow := fs.Duration("for", 0, "how long to force the window open (e.g. 90m); 0 = until further notice")
	rest := args[1:]
	// Split flags from positionals before parsing: Go's flag package stops at
	// the first non-flag argument, so `queue priority 583 last --server …` would
	// otherwise never see --server.
	valueFlags := map[string]bool{"config": true, "server": true, "token-file": true, "for": true}
	var flagArgs, positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") {
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
		return ExitInvalid
	}
	client := newAPIClient(cfg, *serverURL, *tokenFile)

	switch verb {
	case "pause", "resume":
		var out map[string]any
		code, err := client.do(http.MethodPost, "/api/v1/control/pause",
			map[string]any{"paused": verb == "pause"}, &out)
		if err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		return printControl(stdout, stderr, jsonOut, code, out)

	case "open", "close", "auto":
		minutes := int((*forHow).Minutes())
		var out map[string]any
		code, err := client.do(http.MethodPost, "/api/v1/control/window",
			map[string]any{"action": verb, "minutes": minutes}, &out)
		if err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		return printControl(stdout, stderr, jsonOut, code, out)

	case "retry", "skip", "priority":
		if len(positional) == 0 {
			say(stderr, "queue %s needs a file id\n", verb)
			return ExitUsage
		}
		idArgs := positional
		var priority int
		if verb == "priority" {
			// `queue priority 42 next|normal|last|<n>`
			if len(positional) < 2 {
				say(stderr, "queue priority needs an id and a value\n")
				return ExitUsage
			}
			p, err := parsePriority(positional[len(positional)-1])
			if err != nil {
				say(stderr, "%v\n", err)
				return ExitUsage
			}
			priority = p
			idArgs = positional[:len(positional)-1]
		}
		ids := make([]int64, 0, len(idArgs))
		for _, p := range idArgs {
			id, err := strconv.ParseInt(p, 10, 64)
			if err != nil {
				say(stderr, "%q is not a file id\n", p)
				return ExitUsage
			}
			ids = append(ids, id)
		}
		return act(stdout, stderr, client, verb, ids, priority, jsonOut)

	default:
		say(stderr, "unknown queue verb %q\n", verb)
		return ExitUsage
	}
}

func act(stdout, stderr io.Writer, client *apiClient, action string, ids []int64, priority int, jsonOut bool) int {
	var out struct {
		Applied int `json:"applied"`
		Refused []struct {
			ID     int64  `json:"id"`
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"refused"`
	}
	code, err := client.do(http.MethodPost, "/api/v1/queue/act",
		map[string]any{"action": action, "ids": ids, "priority": priority}, &out)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitRuntime
	}
	if jsonOut {
		writeJSON(stdout, out)
		if out.Applied == 0 && len(out.Refused) > 0 {
			return ExitRuntime
		}
		return ExitOK
	}
	say(stdout, "%s: %d applied\n", action, out.Applied)
	for _, r := range out.Refused {
		state := r.State
		if state == "" {
			state = r.Reason
		}
		say(stdout, "  #%d refused (%s)\n", r.ID, state)
	}
	_ = code
	if out.Applied == 0 && len(out.Refused) > 0 {
		return ExitRuntime
	}
	return ExitOK
}

func printControl(stdout, stderr io.Writer, jsonOut bool, status int, out map[string]any) int {
	if jsonOut {
		writeJSON(stdout, out)
		return ExitOK
	}
	ctl, _ := out["control"].(map[string]any)
	if ctl == nil {
		say(stdout, "HTTP %d\n", status)
		return ExitOK
	}
	if paused, _ := ctl["paused"].(bool); paused {
		say(stdout, "queue paused\n")
	} else {
		say(stdout, "queue running\n")
	}
	switch ctl["override"] {
	case "open":
		say(stdout, "window forced open (%s)\n", describeUntil(ctl["override_until"]))
	case "close":
		say(stdout, "window forced shut: no new claims\n")
	default:
		say(stdout, "window follows the schedule\n")
	}
	return ExitOK
}

func describeUntil(v any) string {
	f, ok := v.(float64)
	if !ok || f <= 0 {
		return "until further notice"
	}
	return "until " + time.Unix(int64(f), 0).Format("15:04")
}

func parsePriority(s string) (int, error) {
	switch strings.ToLower(s) {
	case "next", "front", "first":
		return 100, nil
	case "normal", "default":
		return 0, nil
	case "last", "back":
		return -100, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not next, normal, last or a number", s)
	}
	return n, nil
}
