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
| `web/` | the embedded React app: `web.go` serves `app/dist`, which `make web` builds |
| `deploy/` | container + host deploy assets |
| `config.example.json` | rule list and settings shape |
| `bin/` | build output (never committed) |
| `MESSER.md` | the UI's adoption record: brand, canon version, atom patches, deviations |

## Commands

```bash
make build      # bin/messer-transcode with version/commit/date ldflags (builds the frontend first)
make web        # vite build → web/app/dist (embedded, so this is a prerequisite of any go build)
make web-test   # vitest: formatters, state map, schedule copy
make web-dev    # vite on 5299, proxying /api to the Go server on 8299
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
- **The UI never takes the orchestrator down.** The React app is embedded (`//go:embed all:app/dist`);
  with no `dist` the HTML routes answer `503 frontend not built: run make web` and the node API keeps
  serving. A missing frontend is a degraded UI, not a stopped pipeline.
- **Never bind a local dev server to 8099.** That is the container's published port *and* the node's
  configured `server.url` (`http://127.0.0.1:8099`), so a host process there answers the live node's
  registration and silently takes the encoder out of service — it did exactly that for one window.
  Vite proxies `/api` to `VITE_API_ORIGIN`, default `http://127.0.0.1:8299`.
- **The dev server is a PM2 pair, started from the Portal, not from a shell.** `transcode-dev` runs
  `~/Scripts/run-transcode-dev.sh` (Go :8299 + Vite :5299, one PM2 app whose trap kills both) plus
  `transcode-dev-watch` (idle scale-to-zero). Served at `transcode-dev.messer.wtf` / `transcode-dev.local`.
  Config and queue are snapshots in `.dev/` (gitignored), never the production database. A dev server
  started by hand is one that outlives the session that started it — `~/Scripts/dev-stray-guard.py
  --sweep --repo messer-transcode` reaps any that appear.

## Working rules

- Version comes from git tags; a release tag also publishes a pinned image tag.
- `doctor` reports one root cause, not a cascade: dependent checks are skipped when a prerequisite fails.
- Anything that used to be a Tdarr question is this project's question — Tdarr is gone.

## Where the depth is

Skill `messer-transcode` — operating the pipeline, queue stalls, encoder/quality decisions, the
container+node oracle, and the reference files for DB queries and encoder settings.
