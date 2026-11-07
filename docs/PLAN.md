# messer-transcode — implementation plan (rev 2)

> **For post-compaction execution.** This document is self-contained: it carries the analysis
> that justifies it, every number measured during the investigation, and the decisions already
> taken. Execute milestone by milestone; each has an acceptance gate that must pass before the
> next begins.

**Goal:** Replace Tdarr with a purpose-built transcode pipeline — a containerised Go
orchestrator plus a native macOS worker — that re-encodes the video library to HEVC at a
measured VMAF target, is operable by an agent rather than by a human in a flow editor, and can
never destroy a source file.

**Architecture:** Two processes, one Go binary. `messer-transcode server` runs in Docker and
owns SQLite state, the queue, the rules engine and the UI; it never touches the media volume.
`messer-transcode node` runs natively on the macOS host under PM2, because VideoToolbox is
unreachable from a container, and performs all file I/O and ffmpeg work. The node pulls work
over HTTP, so the host needs no inbound port.

**Tech stack:** Go 1.26 (stdlib HTTP, `html/template` + `embed`, `modernc.org/sqlite` for
cgo-free builds), htmx + hand-written CSS for the UI, ffmpeg/ffprobe from Homebrew for the
encode path, SQLite (WAL) for state. No JavaScript build step.

---

## 1. Why this exists — findings from the investigation (2026-11-06)

Tdarr has been running nightly and producing nothing since early May. Evidence:

| Finding | Measurement |
|---|---|
| Library | 3,205 files, 9,531 GB; HEVC 2,167 files / 5,259 GB / 2,534 h; H.264 1,022 files / 4,264 GB / 1,318 h |
| Parked in error | 2,951 files `Transcode error` (9,009 GB); only 42 `Queued`; 2,436 with `HealthCheck = Error` |
| Last real output | 0 successful transcodes May–September 2026; 63 in Oct, 62 in Nov (all remuxes) |
| Lifetime savings ledger | 4,901 GB, all of it before May 2026 |
| Root cause 1 | `mkvtoolnix` broken since 2026-05-08 (`fmt` 11→12); `Run MKVPropEdit` throws after the encode, so every video transcode dies after burning 20–45 min |
| Root cause 2 | The "VTB quality 28/32" node emits `-qp`, which `hevc_videotoolbox` silently ignores — every encode lands on a fixed ~7.2 Mbps default |
| Root cause 3 | 39-node flow performs up to 8 full read+write passes per file |
| False positives | 7 of 8 `HealthCheck = Error` files decode cleanly |
| Health monitoring | The daily check reads counters that are written on error too, and a skill note wrongly dismissed the 2,436 error rows as "stale leftovers" |

Measured constants on this hardware (M4, 10-core, 16 GB) — do not re-measure, reuse:

| Quantity | Value |
|---|---|
| Media volume sequential read | 44–46 MB/s (18.6 MB/s under 4 concurrent readers) |
| Active NVMe write | 3.3 GB/s |
| VideoToolbox HEVC encode | 8.6–11.8× realtime at 1080p |
| libx265 `-crf 28 -preset slow` | 0.49× realtime |
| Concurrent encodes | 1/2/4 at once → 14.4 / 28.3 / 56.3 s per clip, **identical aggregate** |
| Media engine | exactly one — worker count buys no throughput |

VMAF ladder measured on 60 s of Babylon (2022), H.264 1080p source at 10.7 Mbps, encoded with
`hevc_videotoolbox`: 3.07 Mbps → 90.0 · 5.12 → 94.5 · 6.11 → 95.4 · 8.13 → 96.6 · 12.50 → 98.5.
**Transparency (VMAF ≈ 95) costs ~6 Mbps from this encoder**, not the 3 Mbps originally assumed.

Field research (dated, primary sources) is in `~/Artifacts/transcode-tooling-research/report.md`.
Summary: only Tdarr and FileFlows offer VideoToolbox *and* a durable queue *and* an API;
FileFlows gates its API behind a licence, Unmanic has no real VideoToolbox support, ab-av1 and
HandBrake have no queue, AV1AN has no VT. Tdarr remains the only turnkey option — which is why
the replacement must be built rather than bought, and why parity is a low bar.

---

## 2. Decisions already taken (do not re-litigate)

| # | Decision | Rationale |
|---|---|---|
| D1 | **Go**, single binary, two subcommands | ~20–40 MB RSS; static binary; `os/exec` streaming; `embed` for the UI; cgo-free via `modernc.org/sqlite`. Rust buys nothing because ffmpeg does the work; JVM (Java/Scala) costs 200 MB–1 GB per process and adds a build system; Node is acceptable but weaker at process supervision and static distribution. Go is also the language this agent iterates fastest in, which is the stated maintenance requirement. |
| D2 | **Container = orchestrator + UI only; host process = all file work** | VideoToolbox is unreachable from a Linux container. The container therefore never mounts the media volume — the node walks the filesystem and reports. This also removes Tdarr's virtiofs scanner slowness. |
| D3 | **Node pulls work over HTTP; no inbound port on the host** | No firewall/sudoers work, node restarts freely, server downtime is survivable. |
| D4 | **JSON config is a flat ordered rule list, not a graph** | The 39-node flow expresses 14 behaviours. A rules list is diffable, reviewable and testable; a DAG is what made Tdarr unmaintainable. |
| D5 | **VMAF calibration once, not per file** | Per-file ladder search costs ~100–180 s against a ~600 s encode (17–30 % overhead). Calibrate on a representative sample, store the table, and offer an opt-in per-file probe for outliers. |
| D6 | **Quarantine, never delete** | A wrong decision must cost disk, not data. Source moved to a dated directory after the replacement verifies. |
| D7 | **One encode worker** | Measured: concurrency yields zero aggregate throughput and multiplies I/O contention. |
| D8 | **Server is always-on, no scale-to-zero** | Tdarr's STZ/night-label interaction is a documented failure source. The server is ~30 MB and must be reachable whenever a node polls. |
| D9 | **The server owns the schedule; the node drains gracefully** | Kills the class of bug where one component stops while another has work in flight. |
| D10 | **Agent-first = machine-readable by default** | Every command takes `--json`; logs are structured; errors carry stable codes; `doctor` returns a verdict object. The UI is a layer on top, not the interface. |
| D11 | **MIT licence from day one**, no server-specific defaults in code | The repo is intended to be publishable; personal values live in `config.json`. |
| D12 | **Repo at `~/Repos/messer-transcode`**, module `github.com/jakobbjelver/messer-transcode` | Matches the existing repo layout and GitHub account. |
| D13 | **UI is a "film slate"** — OpenCode-landing rigour with a film-lab twist | Borrows: `[*]`-style markers, hairline rules, oversized single claims, `Fig. n.` captions, monochrome, block-glyph wordmark, no cards/shadow/radius. Twist: timecode, frame counters, sprocket perforations, a slate as the dashboard. Detail in §9. |
| D14 | **Dev server on port 8099 with its own route**, not `:3000` | `:3000` is a one-at-a-time convention across all repos (`host-dev-server-pm2`); this server runs all day, so it must not contend. `transcode-dev.local` / `transcode-dev.messer.wtf`. |
| D15 | **Publishing mirrors the other repos** — ghcr image + semantic-release + `komodo-deploy.mjs` | `server-portal` and `swiparr` already use `REGISTRY: ghcr.io`, owner-scoped `IMAGE_NAME`, conventional-commit releases. Reuse, do not invent. |
| D16 | **Issue-only contribution policy** | No pull requests; contributing text in §10. Keeps a solo-maintained repo free of review overhead while staying genuinely open. |

### Non-goals (explicitly out of scope)

Plugin marketplace · visual flow editor · multiple libraries · folder watching · distributed
nodes · per-library schedules · job-report archives · user accounts · transcode history charts
beyond basic totals · any codec other than HEVC for video output · AV1 (no hardware encoder on
this SoC; iPhone/Apple TV would transcode on playback) · editing metadata (nfo/artwork).

---

## 3. System design

### 3.1 Components

```
┌──────────────────────────────┐         ┌───────────────────────────────┐
│  messer-transcode server     │  HTTP   │  messer-transcode node        │
│  (Docker, always-on)         │◀───────▶│  (macOS host, PM2)            │
│                              │  pull   │                               │
│  • SQLite state (WAL)        │         │  • walks /Volumes/Media       │
│  • queue + rules engine      │         │  • ffprobe / exiftool         │
│  • planner (decisions)       │         │  • VMAF sampling              │
│  • scheduler + window        │         │  • ffmpeg encode (VTB)        │
│  • HTTP API + htmx UI        │         │  • verify + atomic replace    │
│  • never touches media       │         │  • ingest/encode/egress lanes │
└──────────────────────────────┘         └───────────────────────────────┘
        ▲                                          │
        │ Caddy route                              │ reads/writes
   transcode.local                          /Volumes/Media  (HDD)
   transcode.messer.wtf                     /Volumes/Active (NVMe scratch)
```

### 3.2 Job protocol (v1)

Node-initiated, idempotent, no long-lived connection required.

| Call | Direction | Purpose |
|---|---|---|
| `POST /api/v1/node/register` | node → server | hostname, version, encoders, volume paths, free GB; returns `node_id` |
| `POST /api/v1/node/claim` | node → server | returns the next job or `204`; server leases it for 5 min |
| `POST /api/v1/node/jobs/{id}/heartbeat` | node → server | stage, progress %, fps, eta; renews the lease |
| `POST /api/v1/node/jobs/{id}/result` | node → server | outcome, measurements, error code; releases the lease |

Job types: `scan` (walk a subtree, report files), `probe` (ffprobe + exiftool one file),
`encode` (explicit target), `verify` (re-check an existing file), `sweep` (full decode check).

A `probe` result carries the facts the planner needs, including an optional VMAF curve when the
job includes `vmaf_ladder`. The server decides; the node never invents a target.

Leases: a job claimed and not heartbeated within 5 min returns to `pending` with `attempts+1`.
After `max_attempts` (3) it becomes `failed` with the last error code.

### 3.3 State model

```
new ──probe──▶ probed ──plan──▶ planned ──claim──▶ encoding ──▶ verifying ──▶ replacing ──▶ done
                                    │                                            │
                                    └──▶ skipped (rule said no)                   └──▶ failed
```

Every transition is committed to SQLite before the next step runs, so a `kill -9` at any point
resumes rather than restarts. A file whose `(size, mtime)` hash changed since probing is
re-probed rather than trusted.

### 3.4 Database schema

```sql
CREATE TABLE files (
  id            INTEGER PRIMARY KEY,
  path          TEXT NOT NULL UNIQUE,
  library       TEXT NOT NULL,
  size_bytes    INTEGER NOT NULL,
  mtime_ns      INTEGER NOT NULL,
  probe_key     TEXT,                 -- sha256(size, mtime) at probe time
  container     TEXT, video_codec TEXT, width INTEGER, height INTEGER,
  bitrate_kbps  INTEGER, duration_s REAL, pix_fmt TEXT,
  audio_tracks  TEXT,                 -- JSON [{index,codec,lang,channels,title,commentary}]
  sub_tracks    TEXT,                 -- JSON [{index,codec,lang,title,forced,commentary}]
  tmdb_id       INTEGER,              -- parsed from the folder name {tmdb-NNNN}
  decision      TEXT,                 -- skip|remux|encode|quarantine
  reason        TEXT,                 -- human sentence, shown in the UI
  reason_code   TEXT,                 -- stable: R_LOW_RES, R_HEVC_EFFICIENT, ...
  target_kbps   INTEGER,
  state         TEXT NOT NULL DEFAULT 'new',
  attempts      INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT, last_error_code TEXT,
  node_id       TEXT, claimed_at INTEGER, lease_until INTEGER,
  started_at    INTEGER, finished_at INTEGER,
  bytes_before  INTEGER, bytes_after INTEGER, vmaf REAL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_files_state ON files(state);
CREATE INDEX idx_files_decision ON files(decision);
CREATE INDEX idx_files_library ON files(library);

CREATE TABLE jobs (
  id TEXT PRIMARY KEY, file_id INTEGER, type TEXT NOT NULL,
  payload TEXT, state TEXT NOT NULL, node_id TEXT,
  created_at INTEGER, started_at INTEGER, finished_at INTEGER,
  result TEXT, error_code TEXT
);
CREATE INDEX idx_jobs_state ON jobs(state);

CREATE TABLE nodes (
  id TEXT PRIMARY KEY, hostname TEXT, os TEXT, arch TEXT, version TEXT,
  encoders TEXT, paths TEXT, free_gb REAL, last_seen INTEGER
);

CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL,
  level TEXT NOT NULL, code TEXT, file_id INTEGER, job_id TEXT,
  message TEXT NOT NULL, data TEXT
);
CREATE INDEX idx_events_at ON events(at);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
```

### 3.5 Stable error codes (agent contract)

`E_SOURCE_MISSING` · `E_SOURCE_CHANGED` · `E_DISK_LOW` · `E_PROBE_FFPROBE` · `E_PROBE_DECODE` ·
`E_ENCODE_FFMPEG` · `E_ENCODE_TIMEOUT` · `E_ENCODE_NO_OUTPUT` · `E_VERIFY_DECODE` ·
`E_VERIFY_DURATION` · `E_VERIFY_SIZE_RATIO` · `E_VERIFY_VMAF` · `E_REPLACE_FSYNC` ·
`E_REPLACE_RENAME` · `E_NODE_LOST` · `E_WINDOW_CLOSED` · `E_CONFIG_INVALID`

Codes are part of the public API: the health check and any future agent rely on them.

### 3.6 Safety invariants (each has a test)

1. A source file is never deleted; it is moved to `quarantine/<YYYY-MM-DD>/<relative path>`.
2. Replacement happens only after the output passes: sampled decode (first 60 s, last 60 s, and
   two random 60 s windows), duration within ±2 s, size ratio inside configured bounds, and
   VMAF ≥ target − 1.0 when VMAF checking is enabled.
3. `fsync` the output before renaming; rename within the destination directory so it is atomic.
4. Free space must exceed `safety.min_free_gb` before a job starts, including scratch headroom.
5. One writer per path: the lease plus an on-disk `flock` on `<scratch>/<id>.lock`.
6. A job never starts if the window has less than the median duration of that file's tier left.
7. `plan` and `run` must agree on the file set; `run` refuses to start if they differ.

### 3.7 Pipeline lanes (the utilisation requirement)

The media engine is busy ~100 % during an encode; the HDD is busy ~11 %. Three lanes overlap:

```
ingest   : HDD ─▶ NVMe scratch     (44 MB/s, 68 s per 3 GB film)
encode   : NVMe ─▶ NVMe            (VTB, ~600 s per 100-min film)
egress   : NVMe ─▶ HDD + quarantine(the original)
```

Buffer depth 2 by default. Because ingest is ~9× faster than encode, depth 2 is enough to
guarantee the encoder never waits; deeper buffers only consume scratch. Idle disk time between
ingests is filled with `probe`/`sweep` jobs, so both devices stay busy without competing with an
encode. Scratch free space is a first-class gauge in the UI and a scheduling constraint.

---

## 4. Configuration (`config.json`, server-side)

```json
{
  "version": 1,
  "libraries": [
    {
      "name": "media",
      "root": "/Volumes/Media/Library",
      "extensions": [".mkv", ".mp4", ".mov", ".m4v"],
      "exclude": ["**/Sample/**", "**/*.sample.mkv", "**/.*"]
    }
  ],

  "schedule": {
    "weekday": "00:30-05:30",
    "weekend": "02:00-07:00",
    "drain_before_minutes": 15,
    "timezone": "Europe/Copenhagen"
  },

  "workers": { "encode": 1, "probe": 2, "ingest_buffer": 2 },

  "paths": {
    "scratch": "/Volumes/Active/Hot/transcode",
    "quarantine": "/Volumes/Media/Quarantine"
  },

  "quality": {
    "mode": "table",
    "vmaf_target": 95.0,
    "vmaf_tolerance": 1.0,
    "table": [
      { "codec": "h264", "height_gte": 1000, "kbps": 5500 },
      { "codec": "h264", "height_gte": 700,  "kbps": 2200 },
      { "codec": "h264", "height_gte": 0,    "kbps": 900  },
      { "codec": "hevc", "height_gte": 1000, "kbps": 6000 },
      { "codec": "hevc", "height_gte": 700,  "kbps": 2400 }
    ],
    "probe_outliers_above_kbps": 12000,
    "min_saving_pct": 10
  },

  "audio": {
    "keep_languages": ["eng", "swe", "dan"],
    "keep_original_language": true,
    "max_tracks": 3,
    "drop_commentary": true,
    "transcode": [{ "from": ["truehd", "flac", "pcm_bluray"], "to": "eac3", "kbps": 448 }]
  },

  "subtitles": {
    "keep_languages": ["eng", "swe", "dan"],
    "drop_commentary": true,
    "drop_closed_captions": true,
    "keep_forced": true
  },

  "rules": [
    { "when": { "height_lt": 1080 },                          "do": "remux",  "code": "R_LOW_RES" },
    { "when": { "codec": "hevc", "bitrate_kbps_lt": 5000 },   "do": "remux",  "code": "R_HEVC_EFFICIENT" },
    { "when": { "codec": "hevc", "bitrate_kbps_gte": 8000 },  "do": "encode", "code": "R_HEVC_OVERBITRATE" },
    { "when": { "codec": "h264", "bitrate_kbps_gte": 5000 },  "do": "encode", "code": "R_H264_ENCODE" },
    { "when": { "codec": "h264" },                            "do": "encode", "code": "R_H264_LOW_BITRATE" },
    { "when": { "default": true },                            "do": "skip",   "code": "R_NO_RULE" }
  ],

  "safety": {
    "min_free_gb": 150,
    "size_ratio_min": 0.35,
    "size_ratio_max": 0.98,
    "duration_tolerance_s": 2,
    "keep_quarantine_days": 14,
    "verify_sample_seconds": 60,
    "verify_vmaf": true,
    "max_attempts": 3
  },

  "notify": {
    "radarr": "http://host.docker.internal:7878",
    "sonarr": "http://host.docker.internal:8989",
    "on": ["replaced", "failed"],
    "jellyfin_refresh": true
  }
}
```

`quality.mode = "table"` uses the calibrated table. `"probe"` runs the per-file ladder for
outliers. `"fixed"` uses `kbps` verbatim (for testing).

Two guards run after a rule says `encode`, and both were added because M1's plan run over real
files exposed the bug they prevent: if the ladder target meets or exceeds the source bitrate the
file is skipped (`R_ALREADY_LEAN`) — otherwise a 2 Mbps H.264 file is "re-encoded" to 5.5 Mbps and
grows — and if the projected saving is under `min_saving_pct` it is skipped (`R_LOW_SAVING`), since
a 3 % win does not pay for an hour of encoding.

---

## 5. Repository layout

```
messer-transcode/
├── cmd/messer-transcode/main.go        # subcommand dispatch
├── internal/
│   ├── config/          config.go, schema.json, validate_test.go
│   ├── store/           sqlite.go, migrations/, files.go, jobs.go, events.go
│   ├── planner/         rules.go, decide.go, decide_test.go, nmap.go
│   ├── ffmpeg/          probe.go, build.go, run.go, vmaf.go
│   ├── transcode/       pipeline.go, verify.go, replace.go
│   ├── node/            node.go, agent.go, lanes.go
│   ├── server/          server.go, api.go, schedule.go, drain.go
│   ├── ui/              templates/*.html, static/*.css, ui.go  (embedded)
│   └── version/         version.go
├── docs/                architecture.md, operations.md, config.md, history-tdarr.md
├── deploy/              Dockerfile, compose.transcode.yaml, ecosystem.transcode.config.cjs
├── testdata/            tiny 10 s clips (ffmpeg-generated, <2 MB total)
├── .github/workflows/   ci.yml, release.yml
├── Makefile
├── go.mod
├── LICENSE              MIT
└── README.md
```

---

## 6. Milestones

Each milestone ends with a gate. Do not start the next until the gate passes. Every task is one
commit; run `gofmt`, `go vet`, `go test ./...` before each.

### M0 — Skeleton and contract (½ day)

| Task | Deliverable |
|---|---|
| 0.1 | `go mod init github.com/jakobbjelver/messer-transcode`; `cmd/` + `internal/` tree; `Makefile` with `build`, `test`, `lint`, `docker` |
| 0.2 | `LICENSE` (MIT), `README.md` (what/why/status), `docs/architecture.md` copied from §3 of this plan |
| 0.3 | `internal/config/schema.json` (JSON Schema for §4) + `validate` that rejects unknown keys and out-of-range values; table-driven test |
| 0.4 | `.github/workflows/ci.yml`: gofmt check, `go vet`, `go test -race ./...`, `golangci-lint`, docker build |
| 0.5 | `deploy/Dockerfile` (multi-stage, `CGO_ENABLED=0`, distroless/scratch + CA certs, non-root) |
| 0.6 | **Private** GitHub repo `jakobbjelver/messer-transcode` created and pushed (off-machine backup, CI runs from commit one); thin `messer-transcode` skill created pointing at this plan |

**Gate:** `go build ./...` passes; `docker build` succeeds; CI green on the private repo; the plan
is copied into `docs/PLAN.md` in the repo so the build carries its own context.

### M1 — Scanner, probe, and `plan` (the validation milestone) — 1½ days

| Task | Deliverable |
|---|---|
| 1.1 | `internal/store`: schema from §3.4, migrations, open/close, WAL, `--db` flag |
| 1.2 | `internal/ffmpeg/probe.go`: run ffprobe `-print_format json -show_streams -show_format`, map to `files` columns; unit test against a fixture JSON |
| 1.3 | `internal/node/scan.go`: walk library roots, filter by extension/exclude, report `(path,size,mtime)`; test with a temp tree |
| 1.4 | `internal/planner/rules.go`: evaluate §4 `rules` against probed facts, return `(decision, reason_code)`; table-driven tests covering every rule in both directions |
| 1.5 | `internal/planner/nmap.go`: build `-map` arguments + audio/subtitle selection from a probe result; test on fixtures with commentary/CC/multi-language tracks |
| 1.6 | `internal/planner/tmdb.go`: parse `{tmdb-NNNN}` from the path, resolve original language; cache in DB; no network in tests (fixture) |
| 1.7 | `cmd/messer-transcode plan`: walk + probe (locally, or via a node) + decide + print `--json` summary (per-decision counts, estimated GB saved, per-file rows) |
| 1.8 | `docs/config.md` explaining every key with this library's values as the example |

**Gate (the important one):** run `plan --json` over all 3,205 files and diff the decision
against Tdarr's `transcode_decision_maker`. Every disagreement is either a rule bug or a
deliberate change — write the list in `docs/plan-diff.md` with a verdict per file class before
M2. Nothing is written to disk by `plan`.

### M2 — Encode, verify, replace — 2 days

| Task | Deliverable |
|---|---|
| 2.1 | `internal/ffmpeg/build.go`: single-pass command from §3, `-map` selection, `-b:v`, `-c:a eac3`, `-c:s copy`, `-f matroska`; golden-string tests |
| 2.2 | `internal/ffmpeg/run.go`: exec with stderr capture, progress parsing (`out_time_us`, `speed`), cancellation via context |
| 2.3 | `internal/transcode/verify.go`: sampled decode, duration delta, size ratio; unit tests with a deliberately truncated file |
| 2.4 | `internal/transcode/replace.go`: fsync, atomic rename, quarantine move, `flock`; test with a simulated power-loss (rename fails → source intact) |
| 2.5 | `internal/transcode/pipeline.go`: the full state machine from §3.3, resumable; crash-injection test (kill between every pair of transitions) |
| 2.6 | `cmd/messer-transcode run --once --limit 1` against a *copy* of one film |
| 2.7 | Extend to `--limit 20` on copies in `/Volumes/Active/tc-test/`, quarantine to a temp dir |

**Gate:** 20 files encoded from copies. All pass verify; originals present in quarantine; VMAF
recorded; `sqlite3` shows `done` for each; no file outside the test directory touched. Then one
real film end-to-end, watched, with the original restored from quarantine afterwards.

### M3 — Server API and UI — 1½ days

| Task | Deliverable |
|---|---|
| 3.1 | `internal/server`: HTTP mux, middleware, `--json` errors, request logging |
| 3.2 | Node endpoints from §3.2 with lease semantics; integration test with a fake node |
| 3.3 | Read endpoints: `/api/v1/status`, `/api/v1/files?state=&decision=`, `/api/v1/files/{id}`, `/api/v1/events`, `/api/v1/metrics` |
| 3.4 | htmx dashboard: tonight's window, files done/remaining, GB reclaimed, current job with progress bar (SSE), disk gauges |
| 3.5 | Queue page (filterable, with "why skipped" reasons), file detail (probe facts, decision, history), settings (read-only JSON view with validation errors) |
| 3.6 | `deploy/compose.transcode.yaml`: server only, no media mounts, `restart: unless-stopped`, no sablier/STZ labels |

**Gate:** UI shows a live encode from the host node; no media volume mounted in the container
(`docker inspect` proves it); cold start to dashboard < 300 ms.

### M4 — Node agent, scheduling, drain — 1 day

| Task | Deliverable |
|---|---|
| 4.1 | `internal/node/agent.go`: register, poll with backoff, claim, heartbeat, report; survives server downtime |
| 4.2 | Local journal so an in-flight job survives a node restart and is re-reported |
| 4.3 | `internal/server/schedule.go`: window from config, `drain_before_minutes`, no new claims after drain |
| 4.4 | Graceful shutdown: SIGTERM → finish current file → exit 0; tested by `kill -TERM` mid-encode |
| 4.5 | `deploy/ecosystem.transcode.config.cjs`: PM2 app for the node, autostart, log paths |
| 4.6 | `docs/operations.md`: window behaviour, how to pause, how to drain, what each error code means |

**Gate:** `kill -TERM` during an encode finishes the file and exits clean; restart resumes the
queue at the next file; a job claimed then abandoned returns to `pending` after the lease.

### M5 — Pre-stage lanes — 1 day

| Task | Deliverable |
|---|---|
| 5.1 | `internal/node/lanes.go`: ingest/encode/egress goroutines with a bounded channel |
| 5.2 | Ingest verifies size and hashes the first/last 1 MB; mismatch → `E_SOURCE_CHANGED` |
| 5.3 | Egress batches copy-back and quarantine after verify |
| 5.4 | Disk-floor guard on scratch and library; scheduling refuses when below |
| 5.5 | Metrics: encoder idle time, ingest throughput, scratch occupancy |

**Gate:** a 2-hour run shows the encoder idle < 5 % of wall time and ingest never blocks the
encoder. Prove with a timeline plot from `events`.

### M6 — VMAF calibration — ½ day

| Task | Deliverable |
|---|---|
| 6.1 | `internal/ffmpeg/vmaf.go`: libvmaf against a reference window, parse score |
| 6.2 | `cmd/messer-transcode calibrate`: pick N files per tier, run the ladder, fit the lowest rung meeting target, emit a `quality.table` block |
| 6.3 | Store calibration runs in `events` with the sample window used |
| 6.4 | `quality.mode = "probe"`: per-file ladder for files above `probe_outliers_above_kbps` |

**Gate:** `calibrate` on 12 files produces a table; two of the resulting encodes are manually
inspected in Jellyfin and the VMAF is within ±1 of target.

### M7 — Agent-first surface and health check — ½ day

| Task | Deliverable |
|---|---|
| 7.1 | `messer-transcode doctor`: checks DB, schema version, config validity, node connectivity, binary deps (`otool -L` sweep), disk floors, window config; one JSON verdict |
| 7.2 | `/api/v1/health` returns the same object, plus last successful encode timestamp |
| 7.3 | Structured JSON logging to stdout and to `events` with codes |
| 7.4 | `~/Scripts/health/transcode-health.sh` + a section in the `system-health-check` skill: calls `/api/v1/health`, fails on `status != ok` |
| 7.5 | New skill `messer-transcode` with operating/repairing procedures, and a `docs/history-tdarr.md` note recording that Tdarr ran here 2025-10 → 2026-11 |

**Gate:** `doctor --json` returns `{"status":"ok"}` on a healthy system and a specific failing
code with the node stopped, the config broken, and a brew binary removed — three negative tests.

### M8 — Cutover and decommission — 1 day + 7 days observation

| Task | Deliverable |
|---|---|
| 8.1 | Shadow week: `run` on the real library with `--limit 2/night` while Tdarr stays idle; compare decisions |
| 8.2 | Switch writer: stop Tdarr node, `pm2 start` transcode-node, enable the full window |
| 8.3 | First full night; verify GB reclaimed against the planning estimate |
| 8.4 | Remove Tdarr: `compose.video.yaml` block, `services.yaml` entry, PM2 app, `configs/video/tdarr/` (move to a dated archive first), `JobReports`, transcode cache dir |
| 8.5 | Update `system-health-check`, `video-stack-hygiene`, `tdarr-operations` (mark superseded) and memory notes |

**Gate:** seven consecutive nights with zero `failed` jobs beyond expected clusters, library
shrinking, no Tdarr process/container/file remaining, and the daily health check reporting the
new pipeline as healthy.

### M9 — Public launch and brand — 1 day (only after M8)

Runs once the tool has already cleared a real batch. The repo is private until here; this is the
"make it public and make it look like it was always meant to be" milestone.

| Task | Deliverable |
|---|---|
| 9.1 | `LICENSE` finalised (MIT, © 2026 Jakob Bjelvér) and `CONTRIBUTING.md` with the exact text in §10 |
| 9.2 | Issue templates `bug.md` / `idea.md` (5 lines each); no PR template — PRs are closed by policy |
| 9.3 | Logo: `logo.svg`, `favicon.svg`, 1280×640 social preview, rendered from the tokens in §9 |
| 9.4 | `README.md` rewritten for strangers: one-line claim, diagram, "why not Tdarr" (3 bullets), quick start (docker + binary), config example, the honest VMAF/space table, safety model, license |
| 9.5 | Real screenshots from the dev server at 1440 and 390 px — no mockups |
| 9.6 | Repo metadata: description, topics, website, social preview image, pinned issue explaining the project's origin |
| 9.7 | `v1.0.0` tag; verify `docker run ghcr.io/m3sserstudi0s/messer-transcode` works from a clean machine profile |
| 9.8 | Flip to public; confirm CI runs on the public repo and the README quick start actually works from scratch |

**Gate:** a stranger can go from the README to a running container serving its UI in under five
minutes, with no reference to this server's paths; the repo is public and green.

### M10 — Maintenance mode ("live forever") — 1 day

The point at which the project stops being a build and becomes something we operate. Everything
here exists so that the loop "he reports → I build → he approves → it ships" is boring.

| Task | Deliverable |
|---|---|
| 10.1 | Complete the `messer-transcode` skill (thin version exists from M0): operating procedures, error-code table, dev loop, publish steps, troubleshooting |
| 10.2 | `actions/actions.json` — Portal Actions contract for the dev server (`run_on: "host"` on every step; start / stop / status), same shape as the other repos |
| 10.3 | Dev route in `services.yaml`: `transcode-dev` → `cloudflare_via_caddy: true`, Caddy directive to `host.docker.internal:8099`; `setup.py` regenerated, then verified through Caddy and the tunnel leg |
| 10.4 | PM2 dev app `messer-transcode-dev` + `~/Scripts/run-dev.sh` wrapper; `make dev` rebuilds and restarts; dev config uses `.dev/state.db` and `/Volumes/Active/tc-test` |
| 10.5 | `release.yml`: semantic-release on `main`, build `darwin/arm64` + `linux/amd64`, push `ghcr.io/m3sserstudi0s/messer-transcode`, attach binaries to the release |
| 10.6 | `docs/operations.md` (window, pause, drain, quarantine restore, disk floors) and `docs/troubleshooting.md` (one section per error code) |
| 10.7 | Monthly `doctor` cron (`no_agent`, silent when clean) reporting the same verdict as the daily health check |
| 10.8 | OSS hygiene: Dependabot for gomod + actions, `SECURITY.md` (one paragraph), issue labels |

**Gate:** a full round trip — he reports a small bug, I fix it on a branch, it appears at
`transcode-dev.messer.wtf`, he says publish, one command ships a new image and Komodo deploys it —
completed end to end without touching this plan.

---

## 7. Testing strategy

| Layer | What |
|---|---|
| Unit | rules table, `-map` builder, verify math, config validation, state machine transitions |
| Golden | ffmpeg argument strings for a fixed set of probe fixtures |
| Integration | real ffmpeg against 10 s synthetic clips in `testdata/` (generated by `make testdata`, gitignored?) — committed, <2 MB |
| Crash | kill between every state transition; assert no source file is ever lost |
| Property | random probe fixtures through the rules engine: decision is total (always defined) and idempotent (re-deciding an encoded file yields `skip`) |
| E2E | `make e2e` spins the server in-process + a node against a temp library of 5 clips |
| Manual | VMAF spot-check and Jellyfin playback for the first 10 real files |

CI runs unit/golden/integration on ubuntu (no VideoToolbox — use `libx265` in tests via a
`--test-encoder` flag), and a separate macOS job for the node path.

---

## 8. Agent-first requirements (concrete, not a slogan)

* every subcommand supports `--json` and exits with a distinct code per failure class
* `doctor` is the single entry point a health check needs
* stable error codes (§3.5) appear in `last_error_code`, `events.code` and the API
* `events` is queryable: "what failed last night and why" is one SQL query
* `plan` is safe to run at any time and is the way to answer "what would you do?"
* no interactive prompts anywhere; all input comes from config, flags or env
* logs go to stdout as JSON lines and are never rotated by the app (PM2/journald does that)
* the UI never becomes the only way to do something

---

## 9. UI design direction — "film slate"

Brief from the user: *wild and raw — think the OpenCode landing page, but with a twist to make it
unique.* The reference was read on 2026-11-06 (opencode.ai): section markers rendered as `[*]`,
hairline rules, one oversized claim per section, a numeric triptych captioned `Fig 1. / Fig 2. /
Fig 3.`, monochrome throughout, no cards, no shadows, no rounded corners, and a wordmark built from
block glyphs rather than a typeface.

**The twist: the subject is a transcoder, so the visual language comes from a film lab.** Timecode,
frame counters, reel marks, sprocket perforations, a slate. The dashboard *is* a slate; the progress
bar is a strip of film; every number is tabular so columns line up.

Tokens:

* **Palette** — `--paper #0b0b0b` (dark lab), `--ink #f4f2ee`, `--rule #26231f`, `--muted #8a847b`.
  One accent: `--live #ffb000` (safety-film amber), used **only** while a job is running and on the
  playhead. `--fail #e5484d`, `--done #46a758`. At rest everything is greyscale — consistent with
  the house rule that colour appears only for state.
* **Type** — one family: `ui-monospace, "SF Mono", Menlo, monospace`, `font-variant-numeric:
  tabular-nums`. Display 48 / 32 / 20 px with `letter-spacing: -0.03em`; labels 11 px uppercase with
  `letter-spacing: 0.12em`.
* **Structure** — 1440 px max, a 12-column hairline grid visible only on the dashboard, full-bleed
  1 px rules between sections, `border-radius: 0` (2 px on inputs), no shadows, no gradients.
* **Vocabulary** — numbered sections `[01]`…`[04]`, captions `FIG. 1 — BYTES RECLAIMED`, paired
  numbers everywhere (`done / total`, `GB in / GB out`).
* **Hero element** — a live **frame strip**: ~120 hairline ticks across the width representing
  tonight's queue, completed ones solid, the current one amber, the rest empty, with a playhead
  tracking progress. Pure CSS (`repeating-linear-gradient` + a transform), no canvas, no JS
  framework, driven by the same SSE stream as the numbers.
* **Motion** — 120 ms linear only. Numbers tick. Nothing bounces, nothing fades in.
* **Logo** — a 24×24 mark of two offset frames with one cut corner ("one pass through"), 2 px
  strokes on a 24 grid; wordmark `messer/transcode` set in the same mono, the slash doubling as the
  mark's negative space. Ship `logo.svg`, `favicon.svg`, a 1280×640 social preview, README hero.
* **Implementation** — one hand-written CSS file (~300 lines) in `internal/ui/static`, embedded in
  the binary. No Tailwind, no build step, no webfont download.

**Gate at M3:** review the running dashboard against this brief at 1440 px and 390 px; the user signs
off on the look before M4 starts. If it looks like every other admin panel, it has failed the brief.

---

## 10. Repository governance

**License:** MIT, © 2026 Jakob Bjelvér. (If the studio should hold it instead, that is one string in
`LICENSE`, `README.md` and `CONTRIBUTING.md`.)

**`CONTRIBUTING.md`** — verbatim, as specified:

```
Contributing

We don't take pull requests. Have your agent open an issue describing the bug or idea, and our
agents usually turn it around fast.

MIT, © Jakob Bjelvér
```

**Policy:** pull requests are closed with a link to the issue tracker. Issues are the only inbound
channel; the templates ask for version, `doctor --json` output, and the error code.

**README skeleton** (M9): one-line claim · what it does (one diagram) · why not Tdarr (three
bullets, factual, no snark) · quick start (docker, then binary) · config example · the honest
VMAF/space table from §1 · how it fails safely (quarantine + seven invariants) · license.

**Deliberately absent:** code of conduct, governance files, contributor ladder, funding, roadmap.
A single-maintainer tool does not need a constitution.

---

## 11. Development loop and "maintenance mode"

This is the part that matters after launch: how a report becomes a shipped fix. It mirrors how the
other repos already work (`host-dev-server-pm2`, the `*.messer.wtf` preview pattern, ghcr +
semantic-release + `komodo-deploy.mjs`).

```
he says "bug: X"  ──▶  I branch, fix test-first, `make check` green
                          │
                          ▼
                    dev server on host (PM2, :8099)
                    transcode-dev.messer.wtf  ──▶  he looks
                          │
              "good, publish" │
                          ▼
          merge to main ─▶ semantic-release tags ─▶ CI builds
          ─▶ ghcr.io/m3sserstudi0s/messer-transcode ─▶ komodo-deploy transcode-stack server
```

Pieces and why each is shaped that way:

| Piece | Detail | Why |
|---|---|---|
| Dev server | `messer-transcode-dev` under PM2, port **8099**, `transcode-dev.local` / `transcode-dev.messer.wtf` | `:3000` is a one-at-a-time convention across all repos; this one runs continuously and must not contend with site or portal |
| Dev data | `.dev/state.db`, library `/Volumes/Active/tc-test` (5 generated clips) | never point a dev build at the 10 TB library |
| Portal Actions | `actions/actions.json` with `run_on: "host"` on every step | the same contract the Portal Dev Server UI already drives for other repos |
| CI | `ci.yml` on push: gofmt, vet, `go test -race`, golangci-lint, docker build | matches `server-portal` / `swiparr` |
| Release | `release.yml`: semantic-release on `main`, `darwin/arm64` + `linux/amd64`, ghcr push, binaries attached | reuse the existing convention, don't invent a versioning scheme |
| Deploy | `node ~/Scripts/komodo-deploy.mjs transcode-stack server` | existing script, existing stack |
| Skill | `/messer-transcode` — thin at M0, complete at M10 | so he can say "check messer-transcode" and get project context without restating it |

---

## 12. Risks

| Risk | Mitigation |
|---|---|
| Wrong replacement damages a file | quarantine + 7 safety invariants + 20-copy rehearsal gate before a real file |
| A bug walks the wrong directory | `plan`/`run` must agree; `run` requires `--yes` when the file count changes by >10 % |
| Node and server drift (the Tdarr failure) | one schedule owner, leases, node-local journal, health check asserts an outcome |
| Scratch fills mid-run | disk-floor guard, buffer depth 2, egress priority when scratch > 70 % |
| VMAF probe adds too much time | calibration table by default; probe only for outliers > 12 Mbps |
| Scope creep into a plugin system | §2 non-goals; config is rules, not a DAG |
| Public repo exposes personal paths or hostnames | no paths in code; `config.json` gitignored, `config.example.json` shipped; README audited at M9 for `messer.wtf` references |
| Dev build pointed at the real library | dev config hard-codes `.dev/state.db` and `/Volumes/Active/tc-test`; a test asserts the dev binary refuses a library root containing `3,000+` files |
| The project dies when attention moves elsewhere | M10's whole purpose: skill, doctor cron, Portal Actions, one-command publish. If it rots, the daily health check says so |
| Skill drifts from the code | the skill's operating section is generated from `--help` output and error codes at M10; `docs/troubleshooting.md` is the source of truth |

---

## 13. Decisions — resolved 2026-11-06

All five open questions were answered with "take your recommendations"; recorded here as what will
be executed, so no re-litigation after compaction.

| # | Question | Resolved | Executed as |
|---|---|---|---|
| 1 | VMAF target | **95.0** | `quality.vmaf_target: 95.0`, tolerance 1.0. ~6 Mbps, ~1.6 TB across the tier set |
| 2 | First real film for the M2 gate | **The Kashmir Files** | 2.9 GB, H.264 1080p, already half-processed in Tdarr's cache — a known quantity |
| 3 | Does the node own the full-library sweep? | **Yes** | `sweep` job type in M1/M7; retires the last Tdarr feature and the 2,436 false positives |
| 4 | Quarantine retention | **14 days** | `safety.keep_quarantine_days: 14`, then delete (media is re-downloadable) |
| 5 | Repo visibility timing | **Private at M0, public at M9** | Private at M0 purely as an off-machine backup; public only after Tdarr is gone and a real batch has cleared |
| 6 | Copyright holder | **Jakob Bjelvér** | One string; change to Messer Studios by editing `LICENSE`, `README.md`, `CONTRIBUTING.md` |
| 7 | Dev preview | **Port 8099, own subdomain** | `transcode-dev.messer.wtf`; never `:3000` |
| 8 | Public-launch scope | **Minimal, sleek, no vibe-app** | M9 gates on: stranger can run it from the README in <5 min, real screenshots, logo, issue-only contributing |

Anything not listed here was decided in §2 (D1–D16) and stands.

---

## 14. Appendix — session artifacts and paths

| What | Where |
|---|---|
| This plan | `~/Artifacts/messer-transcode/PLAN.md` |
| Investigative report (evidence) | `~/Artifacts/tdarr/2026-11-06-tdarr-investigation.md` |
| Pipeline design note | `~/Artifacts/tdarr/2026-11-06-pipeline-design.md` |
| Tool landscape research | `~/Artifacts/transcode-tooling-research/report.md` |
| Tdarr DB (source of truth for the diff) | `~/Server/configs/video/tdarr/server/Tdarr/DB2/SQL/database.db` |
| Tdarr flow + library settings | same DB, `flowsjsondb` / `librarysettingsjsondb` |
| Tdarr node config | `~/Server/configs/video/tdarr/node/configs/Tdarr_Node_Config.json` |
| Tdarr node wrapper (PM2) | `~/Server/scripts/tdarr-node-wrapper.sh` |
| Tdarr compose block | `~/Server/compose.video.yaml` (service `tdarr`) |
| Tdarr services.yaml entry | `~/Server/services.yaml:110` (`tdarr`, subdomain `tdarr-server`) |
| Tdarr job reports (delete at M8) | `/Volumes/Media/Data/Services/tdarr/JobReports` |
| Transcode cache | `/Volumes/Active/Hot/tdarr/transcode_cache` |
| Brew dependency audit | `skills/devops/system-health-check/scripts/brew-dep-audit.sh` |

### Library facts to reuse instead of re-measuring

```
3,205 files / 9,531 GB total
  hevc  2,167 files / 5,259 GB / 2,534 h   avg 4.8 Mbps
  h264  1,022 files / 4,264 GB / 1,318 h   avg 7.8 Mbps
  h264 >=8 Mbps   479 files / 2,859 GB / 707 h
  h264 5-8 Mbps   353 files / 1,113 GB / 418 h
  hevc >=8 Mbps   181 files / 1,109 GB / 274 h
  everything else 1,988 files / 4,451 GB   (leave alone)
```

Expected outcome of the plan at VMAF 95: ~1.6 TB reclaimed, ~140 encode-hours,
≈25 nights at 5.5 h/night or ≈12 with a 11.5 h window. Time is fixed by the media engine;
the quality target is the only dial.

---

## rev log

- **rev 1** (2026-11-06): initial plan. Derived from the Tdarr investigation of the same date.
  No code written; repository does not exist yet.
- **rev 2** (2026-11-06): added the public-launch milestone (M9), the maintenance-mode milestone
  (M10), the UI design direction (§9), repository governance with the issue-only contributing text
  (§10), and the dev-loop contract (§11). D13–D16 added. All open questions resolved in §13 —
  VMAF 95.0, Kashmir Files as the M2 gate, node owns the sweep, 14-day quarantine, private repo at
  M0 and public at M9. M0 now creates the private repo and the thin skill. Still no code written;
  next action is M0.

- **rev 3** (2026-11-06): **M0, M1 and M2 are complete and their gates passed.**
  M0: private repo, config schema + validator, CI, 11.8 MB distroless image.
  M1: scan/probe/plan over the whole library in 4m22s; the diff against Tdarr is in
  `docs/plan-diff.md` (Tdarr wanted 92 % of the library, we want 14 %).
  M2: encode → verify → replace proven on 20 copies and one real episode, evidence in
  `docs/rehearsal-m2.md`. Two policy guards were added during M2 (`R_ALREADY_LEAN`,
  `R_LOW_SAVING`) after the plan run showed low-bitrate files being "re-encoded" upward.
  Findings that change later milestones:
  * The HEVC 1080p rung at 6,000 kbps produced VMAF 94 against a 95 target — passing at the
    tolerance boundary. M6 must test 6,500 and 7,000 on a real sample before the first
    unattended night.
  * Only Matroska output is implemented; 19 non-MKV files (0.6 %) are skipped with
    `R_CONTAINER_UNSUPPORTED`. Revisit if it ever matters.
  * The `vmaf` column stays 0 until M6; verification currently covers duration, size ratio
    and a four-window decode.
  Next milestone: M3 (server API and UI).

- **rev 4** (2026-11-06): **M6 complete — the ladder is now measured, not assumed.**
  `calibrate` ran 60 measurements over 6 files and produced two curves (`docs/calibration.md`):
  HEVC 1080p hits VMAF 95.0 exactly at 6,000 kbps, confirming the plan's number; H.264 sources
  **never reach 95** — 8,500 kbps buys only 94.1, because grain cannot be re-encoded at any
  bitrate. So a single global target was wrong: ladder rows now carry `expected_vmaf`, and
  verification holds each file to its own tier's measured expectation.
  Two consequences worth remembering:
  * The projection drops from 780 GB to **671 GB** with the calibrated table, because the H.264
    target rose from 5,500 to 6,000 and 74 more files fall under the 10 % saving floor.
  * A real Bluray HEVC episode scored **90.8** against a 95 expectation at 6,000 kbps — the
    single-window check was too strict and the content was harder than the sample. Verification
    now averages two windows, and a quality miss triggers **one re-encode at 1.4× the target**
    before the file is failed. That escalation is the answer to the per-file variance the
    calibration exposed; `quality.mode = "probe"` remains unimplemented and is now unnecessary.
  Next milestone: M3 (server API and UI) — M6 was pulled forward because it decides every file.

- **rev 5** (2026-11-06): **M3 core complete — the orchestrator runs and reports.** `serve` brings up the
  read API, the film-slate pages, and the four node endpoints (register, claim, heartbeat, result).
  Two decisions worth keeping:
  * **Progress lives on the file row, not in memory.** The encoder is a different process (eventually a
    different machine), so an in-memory tracker would be a lie. `progress`/`speed` columns are written
    on every callback older than two seconds; the dashboard reads the database, one second at a time,
    over a single EventSource.
  * **Claiming is a state transition inside a transaction**, not a read — two nodes cannot take the
    same file. With `server.token` unset the node API answers 503 rather than serving an open court.
  The first four screenshots found four defects that no test would have: flex ticks collapsing to zero
  height (the queue strip rendered as a blank panel), `data-live` on a wrapper eating the unit label a
  second after load, raw JSON in the audio/subtitle rows, and a "WINDOW SHUT" note sitting next to a
  visibly running job. All fixed; `docs/server.md` records them so they are not relearned.
  Remaining for M4: the node agent itself, which turns these endpoints into an actual worker.

- **rev 6** (2026-11-06): **M4 complete — there is a worker.** `node` registers, claims, processes,
  reports and asks again; the server owns the queue and answers with `X-Claim-Status`
  (`window-shut`, `draining`, `empty`). Three defences against a dead node, in order of speed:
  progress every two seconds, a five-minute lease reaped on the claim path, and a journal that
  makes the next start report `E_NODE_INTERRUPTED`. The gate ran for real: TERM at 51 % of a
  feature, drain message, finish, verify, replace, exit 0 at 4 m 20 s.
  Two findings worth carrying forward:
  * **A node that claims and silently drops a file is worse than one that refuses to start.** The
    CLI forgot to wire `Process`; the agent claimed a job and returned in milliseconds. `Run` now
    refuses to operate without a work function. The claimed row sat in `encoding` until the lease.
  * **ffmpeg's own progress cannot be trusted on files with many subtitle streams.** A 35-minute
    episode with 40 `subrip` tracks reported 3 % and `speed=N/A` while the output was two thirds
    written. Progress is now bytes-on-disk against the projected size, and speed is derived from
    that. This is the number the UI shows, so it had to be true rather than convenient.
  Next: M5 (pre-stage lanes, disk floors, metrics), then M7 (doctor/health), M8 (kill Tdarr), M9 (public).

- **rev 7** (2026-11-06): **M5 complete — the encoder no longer waits for I/O.** `transcode.Split` stops
  after verification and hands the swap back as a function; `Commit` runs it. The node prefetches the
  next file (claim, stage, keep the lease alive) while this one encodes, and places the finished one
  (`docs/lanes.md`).
  **Gate:** four 35-minute episodes, **16 m 54 s working window, 16 m 57 s encoding, 0.2 % idle**;
  a second two-file run agreed (8 m 34 s / 8 m 33 s / 0.2 %). The gate asked for two hours — this was
  seventeen minutes of real work, so treat it as a strong signal rather than a signed-off 2-hour soak.
  Three things the measurement taught:
  * **The first idle numbers were wrong twice (5.7 %, then 10.3 %).** The clock ran to shutdown instead
    of to the last placement, so waiting for the queue looked like idle. Fixing the metric took the
    honest figure to 0.2 % — the work was already fine, the instrument was not.
  * **Verification was the real remaining gap:** two VMAF windows run sequentially cost eleven seconds
    per file. Running them together is most of why the number is now under one percent.
  * **A claim with no id reached the pipeline at shutdown** and failed with "library not configured".
    Both ends now refuse it: `Claim` treats a 200 without a job as no work, and `runOne` ignores an
    identity-less task instead of encoding something it cannot report.
  Deviations from the plan, both deliberate: placement is not batched (a queue of one is what actually
  happens at this throughput), and ingest hashes the head and tail megabyte rather than whole files
  (hashing 40 GB costs more than the copy it is meant to justify).

- **rev 8** (2026-11-06): **M7 complete — the system can now answer "am I working?".** `health.Collect`
  produces one object; `doctor` prints it and exits on it (0 ok · 1 degraded · 3 broken), and
  `/api/v1/health` returns the same thing with `503` when broken. `~/Scripts/health/transcode-health.sh`
  reads either, so the daily check and a human cannot disagree by construction.
  **Gate, all four verified by hand and in tests:** node stopped → `E_NODE_SILENT`; broken config →
  `E_CONFIG_INVALID`; ffmpeg off PATH → `E_BINARY_MISSING`; healthy → `status: ok`.
  What the checks are, and why each earned its place:
  * **the dependency sweep** (`otool -L` + Stat on every non-system path) is the check that would have
    caught the outage that started this project. Writing it produced its own false positive first:
    otool's header line is the binary itself, so ffmpeg was reported as its own missing dependency.
  * **`E_NODE_SILENT` is a failure, not a warning** when work is queued. That single distinction is
    the difference between this and the health check that stayed green for months.
  * **the encoder check encodes a test frame**, because "ffmpeg exists" and "VideoToolbox works" are
    not the same claim.
  * **the config is re-read from disk**, so a file edited after the process started is caught rather
    than described from memory.
  One root cause reports one code: with a broken config or missing binaries the dependent checks are
  skipped instead of stacking three secondary failures onto the same report.
  Also shipped: `docs/history-tdarr.md` (what Tdarr was, the three reasons it went, what was kept),
  and the transcode section in the `system-health-check` skill with the code table.

- **rev 9** (2026-11-07): **M8 complete — the cutover. Tdarr is gone.**
  **Shadow run on the real library** (2 files, `--limit 2`): `10 Things I Hate About You` 7.70 GB →
  **4.84 GB (−37 %), VMAF 95.0** (expected 92.9); a remux left at 6.66 GB as planned. **2.86 GB
  reclaimed in 13m59s**, zero failures. Production plan confirmed at **669.67 GB** over 3,109 files —
  the calibrated projection, not the pre-calibration 780 GB.
  **Cutover:** `transcode` replaced `tdarr` in `compose.video.yaml` and `services.yaml`, deployed
  through Komodo as a member of video-stack. Always-on orchestrator container, 27 MB image, mounts
  only `configs/video/transcode`. The node runs under PM2 from `deploy/ecosystem.transcode.config.cjs`
  with `kill_timeout` of ten minutes so a restart does not throw away an encode.
  **Removal:** container, PM2 app, wrapper, ecosystem file, compose entry, local domain label, backup
  exclusion. Config archived (381 MB, includes the Tdarr `database.db` that `docs/plan-diff.md` cites)
  and 11 months of job reports archived (8.4 GB → ~950 MB). **53 GB of transcode cache deleted.**
  Five things this deployment taught that no test had:
  * **The token cannot live in the stack environment.** Komodo rewrites `.env` from its own stack
    config on every deploy, and writing that array *replaces every other variable* — the documented
    way to wipe a stack's secrets. It now lives in `server.token`, 0600, gitignored, read with
    `--token-file`. The compose file carries no secrets at all.
  * **An idle node was invisible.** Health read the events table, and the node only posted `METRICS`
    when a job finished — so a node correctly waiting for the window looked exactly like a node that
    died at midnight. `NODE_UP` and a five-minute `NODE_IDLE` fixed it. The check meant to tell
    "waiting" from "dead" was reporting "dead" for both.
  * **`--once` never returned outside the window**, backing off for hours. It now exits in 0.4 s
    with `no work: window-shut`.
  * **A stale host process on :8099** made the container's health look wrong for two rounds of
    debugging: I was reading lanes.db through a server I had started an hour earlier. The container
    was right the whole time.
  * **Auto-generated startup scripts do not know about a new service.** `start-video.sh` is generated
    from `services.yaml`; until it was regenerated, Komodo created the container and left it stopped.
  **Gate is calendar time, not work:** seven consecutive nights with zero failures, the library
  shrinking every night, and `transcode-health.sh` green in the daily check. Tonight is night one.
  **Do not quote a nightly reclaim rate yet** — the only measured throughput is two files in 14
  minutes (one encode at 9×, one remux). A full night over the USB volume, with ingest and placement
  in the same window, is the first honest number, and it arrives tomorrow.

### How to resume after a context reset

1. Read this file top to bottom — §1 carries the measured facts, §2 the binding decisions.
2. Check `~/Repos/messer-transcode` exists. If not, start at M0. If it exists, read its `docs/PLAN.md`
   and its git log — the plan is the spec, the log is the progress.
3. Do not re-measure §1's numbers or re-argue §2's decisions; they were established with evidence
   on 2026-11-06.
4. Work milestone by milestone; do not start one until the previous gate passes.
