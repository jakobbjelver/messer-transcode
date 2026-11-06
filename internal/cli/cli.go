// Package cli is the command surface. Every command speaks --json so an agent
// can drive it without parsing prose, and every failure has its own exit code.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/version"
)

// Exit codes are part of the contract: scripts branch on them.
const (
	ExitOK      = 0
	ExitRuntime = 1
	ExitUsage   = 2
	ExitInvalid = 3
)

const usageText = `messer-transcode — transcode orchestrator

usage: messer-transcode <command> [flags]

commands:
  version                 print build metadata
  validate <config.json>  validate a config and report every problem
  schema                  print the JSON Schema for config.json
  help                    this text

flags:
  --json                  machine-readable output

exit codes: 0 ok · 1 runtime error · 2 usage · 3 invalid config
`

// say writes to stdout or stderr. The write error is deliberately ignored: a
// closed pipe is not a reason to fail a command that already did its work.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// Run executes one command and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--json", "-json":
			jsonOut = true
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		say(stderr, "%s", usageText)
		return ExitUsage
	}

	switch rest[0] {
	case "help", "-h", "--help":
		say(stdout, "%s", usageText)
		return ExitOK
	case "version":
		return cmdVersion(stdout, jsonOut)
	case "schema":
		_, _ = stdout.Write(config.Schema())
		say(stdout, "\n")
		return ExitOK
	case "validate":
		if len(rest) < 2 {
			say(stderr, "validate needs a path to config.json\n")
			return ExitUsage
		}
		return cmdValidate(stdout, stderr, rest[1], jsonOut)
	default:
		say(stderr, "unknown command %q\n\n%s", rest[0], usageText)
		return ExitUsage
	}
}

func cmdVersion(stdout io.Writer, jsonOut bool) int {
	if jsonOut {
		writeJSON(stdout, map[string]string{
			"version":    version.Version,
			"commit":     version.Commit,
			"build_date": version.BuildDate,
			"go":         version.Go(),
		})
		return ExitOK
	}
	say(stdout, "messer-transcode %s (%s, built %s, %s)\n",
		version.Version, version.Commit, version.BuildDate, version.Go())
	return ExitOK
}

func cmdValidate(stdout, stderr io.Writer, path string, jsonOut bool) int {
	cfg, err := os.ReadFile(path)
	if err != nil {
		if jsonOut {
			writeJSON(stdout, map[string]any{"ok": false, "error": err.Error()})
		} else {
			say(stderr, "%v\n", err)
		}
		return ExitRuntime
	}

	_, err = config.Decode(cfg)
	if err == nil {
		if jsonOut {
			writeJSON(stdout, map[string]any{"ok": true, "file": path})
		} else {
			say(stdout, "ok  %s\n", path)
		}
		return ExitOK
	}

	if jsonOut {
		out := map[string]any{"ok": false, "file": path, "error": err.Error()}
		if ve := config.AsValidation(err); ve != nil {
			out["problems"] = ve.Problems
		}
		writeJSON(stdout, out)
	} else {
		say(stderr, "invalid  %s\n%v\n", path, err)
	}
	return ExitInvalid
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
