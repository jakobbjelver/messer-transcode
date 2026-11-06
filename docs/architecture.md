# Architecture

Two processes, one binary.

```
┌──────────────────────────────┐         ┌───────────────────────────────┐
│  messer-transcode server     │  HTTP   │  messer-transcode node        │
│  (Docker, always-on)         │◀───────▶│  (macOS host, PM2)            │
│                              │  pull   │                               │
│  • SQLite state (WAL)        │         │  • walks the library roots    │
│  • queue + rules engine      │         │  • ffprobe / exiftool         │
│  • planner (decisions)       │         │  • VMAF sampling              │
│  • scheduler + window        │         │  • ffmpeg encode (VideoToolbox)│
│  • HTTP API + htmx UI        │         │  • verify + atomic replace    │
│  • never touches media       │         │  • ingest/encode/egress lanes │
└──────────────────────────────┘         └───────────────────────────────┘
        ▲                                          │
        │ Caddy route                              │ reads/writes
   transcode.local                          /Volumes/Media  (HDD)
   transcode.messer.wtf                     /Volumes/Active (NVMe scratch)
```

## Why the split is not negotiable

VideoToolbox is a macOS framework. A Linux container cannot call it, and there is no
supported passthrough. So the process that runs `ffmpeg -c:v hevc_videotoolbox` must be
a native macOS process. Everything else — state, scheduling, decisions, the UI — has no
such constraint and belongs in a container where it is disposable.

The corollary is that the container must not mount the media library. The node walks the
filesystem and reports file metadata; the server stores facts and decides. That removes
the slow virtualised scan that made the previous setup's server the bottleneck, and it
means the container can be rebuilt at any time without touching a single media file.

## Job protocol

Node-initiated and lease-based. Nothing listens on the host, so there is no port to
publish and no firewall to adjust.

| Call | Purpose |
|---|---|
| `POST /api/v1/node/register` | hostname, version, encoders, volume paths, free space |
| `POST /api/v1/node/claim` | next job, or `204`; the server leases it for five minutes |
| `POST /api/v1/node/jobs/{id}/heartbeat` | stage, progress, fps, eta; renews the lease |
| `POST /api/v1/node/jobs/{id}/result` | outcome, measurements, error code |

A job claimed and not heartbeated within the lease returns to `pending` with its attempt
count incremented. After `max_attempts` it becomes `failed` carrying its last error code.

## State model

```
new ──probe──▶ probed ──plan──▶ planned ──claim──▶ encoding ──▶ verifying ──▶ replacing ──▶ done
                                    │                                            │
                                    └──▶ skipped (rule said no)                   └──▶ failed
```

Every transition is committed before the next step runs, so a `kill -9` at any point
resumes rather than restarts. A file whose `(size, mtime)` changed since it was probed is
re-probed rather than trusted.

## Safety invariants

Each of these has a test.

1. A source file is never deleted; it moves to `quarantine/<YYYY-MM-DD>/<relative path>`.
2. Replacement happens only after the output passes a sampled decode (first and last
   60 s, plus two random 60 s windows), a duration check within tolerance, a size-ratio
   check, and — when enabled — a VMAF check against the target.
3. The output is `fsync`ed, then renamed within its destination directory so the swap is
   atomic.
4. Free space must exceed the configured floor before a job starts.
5. One writer per path: the lease plus an on-disk lock in scratch.
6. A job does not start if the window has less time left than the file's tier usually takes.
7. `plan` and `run` must agree on the file set; `run` refuses a set that moved by more
   than ten percent.

## Pipeline lanes

The media engine is busy roughly 100 % of the time during an encode; the media volume is
busy about 11 %. Three lanes overlap so the faster device absorbs the slower one's latency:

```
ingest : HDD ─▶ NVMe scratch    (~44 MB/s, ~68 s for a 3 GB film)
encode : NVMe ─▶ NVMe           (VideoToolbox, ~600 s for a 100-minute film)
egress : NVMe ─▶ HDD + quarantine
```

Buffer depth two is enough, because ingest is roughly nine times faster than encode.
Deeper buffers only consume scratch. Disk time left over between ingests is filled with
`probe` and `sweep` jobs, so neither device idles while waiting for the other.

## Concurrency

Measured on this hardware: one, two and four simultaneous encodes produced identical
aggregate throughput (14.4 / 28.3 / 56.3 s for the same work). There is one media engine.
Encoder concurrency is a configuration field, not a performance lever.

## Error codes

Stable, and part of the public API: the health check and any agent branch on them.

`E_SOURCE_MISSING` · `E_SOURCE_CHANGED` · `E_DISK_LOW` · `E_PROBE_FFPROBE` ·
`E_PROBE_DECODE` · `E_ENCODE_FFMPEG` · `E_ENCODE_TIMEOUT` · `E_ENCODE_NO_OUTPUT` ·
`E_VERIFY_DECODE` · `E_VERIFY_DURATION` · `E_VERIFY_SIZE_RATIO` · `E_VERIFY_VMAF` ·
`E_REPLACE_FSYNC` · `E_REPLACE_RENAME` · `E_NODE_LOST` · `E_WINDOW_CLOSED` ·
`E_CONFIG_INVALID`
