// Command messer-transcode is the orchestrator (server), the worker (node) and
// the operator CLI in one binary. See docs/architecture.md.
package main

import (
	"os"

	"github.com/jakobbjelver/messer-transcode/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
