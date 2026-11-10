# AGENTS.md — messer-transcode

Purpose-built Tdarr replacement: one Go binary in two roles — an **orchestrator** in a container that
owns queue, rules and verdict, and a **native macOS worker** on the host that encodes with
VideoToolbox. The container never mounts the media library.

## Read first

`docs/PLAN.md` (and `~/Artifacts/messer-transcode/PLAN.md`) is the spec: measured facts, 16 binding
decisions, schema, API contract, error codes, milestones with acceptance gates, rev log. Do not
re-derive its numbers or re-argue its decisions.

## Layout

| Path | What |
|---|---|
| `cmd/messer-transcode/` | entry point (role selected by flag/config) |
| `internal/` | orchestrator, rules, queue, node/worker, version |
| `deploy/` | container + host deploy assets |
| `config.example.json` | rule list and settings shape |
| `bin/` | build output (never committed) |

## Commands

```bash
make build      # bin/messer-transcode with version/commit/date ldflags
make test       # go test ./...
make race       # race detector
make vet lint check
make docker     # container image
```

## Invariants

- **The container never touches the films.** Only the host worker mounts the media library; anything
  that can damage the library stays out of the container.
- **The queue survives restarts** and lives on the Docker volume — never on a virtiofs/OrbStack bind
  mount, where SQLite dies.
- **Rules are data, not code**: a re-encode policy change is a JSON rule edit plus a measured VMAF
  check, never a hard-coded branch.
- **A verdict comes from an exit code, not from stderr text.** ffmpeg writes the null-muxer DTS
  monotonicity warning on healthy files whose PTS equals DTS, and exits 0. Verify treats a non-zero
  exit as failure, filters that one known line, and keeps everything else fatal.
- **The quarantine is optional; the swap is not.** Replacing is one rename inside the destination
  directory whether or not a copy is kept, so a crash before the rename always leaves the original.
  `safety.quarantine: "off"` gives up only the rollback copy, never the atomicity.
- **A finished row describes the file that is now on disk.** The finish payload carries the output's
  probe and mtime; the scanner compares both, so omitting them makes the next scan re-queue the work
  it just did.
- **History is a fact, not a state.** Runs and the reclaimed ledger are counted from `finished_at`,
  never from `state='done'`, so a later plan cannot rewrite what already happened.
- **Measured numbers are facts** (bitrate, VMAF target, encode time). Re-measure before contradicting
  the plan; do not "improve" a measured value by intuition.
- **Deploy is build → image → Komodo**: push builds `ghcr.io/jakobbjelver/messer-transcode`, then
  `komodo_deploy_stack(stack="video-stack", services=["transcode"])`. The macOS worker is a PM2 job.

## Working rules

- Version comes from git tags; a release tag also publishes a pinned image tag.
- `doctor` reports one root cause, not a cascade: dependent checks are skipped when a prerequisite fails.
- Anything that used to be a Tdarr question is this project's question — Tdarr is gone.

## Where the depth is

Skill `messer-transcode` — operating the pipeline, queue stalls, encoder/quality decisions, the
container+node oracle, and the reference files for DB queries and encoder settings.
