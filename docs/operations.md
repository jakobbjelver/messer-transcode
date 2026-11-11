# Operations

Two processes, one queue.

- **`serve`** — the orchestrator. Runs in the container. Holds the database, the
  pages, and the claim endpoint. Never sees the media volume.
- **`node`** — the worker. Runs natively on the Mac under PM2, because
  VideoToolbox is a host framework a Linux container cannot reach.

Start the server, then the node. The node pulls work; nothing connects inward to
the host.

## The lanes

Ingest and placement never block the encoder: the next file is staged while this
one encodes, and the finished one is placed while the next one encodes. The
details, the measurements and the two metrics that lied are in `docs/lanes.md`.

Watch `events` with code `METRICS` if idle time ever reappears: each job records
`{"encode_s":…,"egress_s":…,"wait_s":…}`, and `wait_s` is the gap that should
never be more than a few milliseconds.

## The window

`schedule.windows` is a list of `{days, start, end}` blocks in
`schedule.timezone`; a block that ends before it starts runs past midnight. Outside the window the server answers claims with
`204` and `X-Claim-Status: window-shut`.

`schedule.drain_before_minutes` is the tail of the window in which no *new*
file is claimed, so a file taken at 05:29 still has time to finish before the
05:45 backup. Inside that tail the answer is `X-Claim-Status: draining`.

A job already running is never killed by the window closing. Throwing away an
hour of encoding to honour a schedule is worse than finishing ten minutes late.

## Deploying

Two halves, deployed differently.

**The orchestrator** is a container in `video-stack`, declared in
`~/Server/compose.video.yaml` and deployed through Komodo like every other
service. It pulls `ghcr.io/jakobbjelver/messer-transcode:latest`, published by
`.github/workflows/image.yml` on every push to `main` — the same pattern as
server-portal and messer-request-games, so no image is ever built by hand on the
host and `GITHUB_TOKEN` supplies the package permission.

Its one mount is `server_configs_video_transcode`, a bind-backed volume over
`~/Server/configs/video/transcode` and created by `scripts/docker/create.sh`
with every other data volume. Config and queue live together there; the
container never sees the media library, and a component that cannot touch the
library cannot damage it.

**Only the container may write the state database.** Host-side `sqlite3` against
a live container corrupts it — measured, 40 of 40 writes failed once both sides
were writing, while container-only writes were clean. To re-plan while the
orchestrator runs, use a one-shot container on the same volume:

```bash
docker run --rm -v server_configs_video_transcode:/config \
  --entrypoint /usr/local/bin/messer-transcode \
  ghcr.io/jakobbjelver/messer-transcode:latest \
  plan --config /config/config.json --db /config/state.db
```

**The node** runs under PM2, because VideoToolbox is not reachable from a Linux
container:

```bash
pm2 start deploy/ecosystem.transcode.config.cjs   # first time, from the repo
pm2 restart transcode-node                        # after make build
pm2 logs transcode-node --lines 40 --nostream
```

**The token.** The node authenticates with a token that must not sit in
`config.json` (committed beside it) or in the stack environment (Komodo rewrites
`.env` from its own stack config on every deploy, and writing that array
replaces every other variable in it). It is `/config/server.token`, mode 0600,
gitignored, and read with `--token-file`:

```bash
openssl rand -hex 32 > ~/Server/configs/video/transcode/server.token
chmod 600 ~/Server/configs/video/transcode/server.token
```

**The address.** `https://transcode.messer.wtf`, behind Cloudflare Access
like the rest of the stack. The Caddyfile and the tunnel are generated from
`services.yaml`, so the subdomain is declared there and nowhere else:

```bash
python3 scripts/setup/setup.py caddy
python3 scripts/setup/setup.py cloudflared
```

## Reporting

```bash
messer-transcode report --last 7          # one line per run
messer-transcode report --last 7 --json   # the same, for scripts
curl -s localhost:8099/api/v1/runs?last=7
```

A **run** is one occurrence of the window in `schedule.*` — not a night. Point
the window at `09:00-17:00` and the report describes that window with the same
columns: files done, encoded vs remuxed, bytes back, VMAF mean and minimum,
share of the window covered, failures and skips by code. Nothing in the schema
or the naming assumes the work happens after dark.

The dashboard states the schedule it cuts runs against, so a changed window is
visible without reading the config.

## The quarantine

`safety.keep_quarantine_days` is the only thing that lets this tool delete
anything, and it deletes only what it put there:

```
Quarantine/2026-11-07/Movies/Some Film (1999)/Some Film.mkv
```

It lives on the **fast volume, not beside the library**, and that is deliberate.
A processed file leaves its original here, so if the quarantine sits on the
library's own disk the volume *grows* by the size of every replacement and the
floor stops the batch within nights. On a separate disk the original leaves the
library entirely: the library shrinks from the first file, and the quarantine
accumulates where there is headroom. The trade is that a failed fast disk costs
the originals, not the films — with a short keep window that is the right way
round.

The node expires those directories once a day, at the first idle moment inside
the window, and reports `QUARANTINE_SWEEP` when it actually removes something. It
runs on the node because the orchestrator never mounts the media volume. A marker
file next to the journal (`quarantine.sweep`) keeps it to once per day across
restarts.

```bash
messer-transcode sweep --config config.json          # dry run, always
messer-transcode sweep --config config.json --apply  # actually delete
```

Only directories named `YYYY-MM-DD` are ever removed. A stray folder, a file, or
a symlink in that directory is reported as ignored and left alone — the tool
deletes what it named, and nothing else.

This is not housekeeping: every re-encode grows the volume by the size of the
file it replaces until that day arrives. Without the sweep a 10 TB library fills
up in a few nights. `doctor` reports `E_SWEEP_STALE` when the oldest directory is
older than the keep window, which is the first sign that the node has stopped.

## Reading the queue from the host

Do not. A host read of the bind-mounted database is **stale while the container
is writing it** — measured at 36 minutes behind, with the API already showing
events the file did not. Reads through virtiofs do not see recent writes, so a
stale view invents failures that are not there (a healthy node reported as
`E_NODE_SILENT`).

`doctor` and `report` therefore ask the orchestrator first and fall back to the
file only when it is unreachable:

```
$ messer-transcode report --last 2
schedule  00:30-05:30 weekdays · 02:00-07:00 weekends · Europe/Copenhagen
source    orchestrator http://127.0.0.1:8099
```

The same rule applies to writes, which are worse: see the note in
`compose.video.yaml`.

## Stopping

    pm2 stop transcode-node      # or: kill -TERM <pid>

One SIGTERM means "finish the current file, then exit". The node stops claiming,
the encode in flight is **not** cancelled, the result is reported, and the
process exits 0. Expect up to one film's worth of delay; `kill_timeout` in
`deploy/ecosystem.transcode.config.cjs` is ten minutes for that reason.

A second SIGTERM exits immediately with code 130. That abandons the file: the
journal records it, and the next start reports `E_NODE_INTERRUPTED`.

## Pausing

    pm2 stop transcode-node

The queue is untouched. Restart and it resumes at the next planned file. To stop
the *server* instead, stop the container: the node sees connection failures,
backs off to two minutes, and keeps waiting rather than exiting.

## When the node dies

Three separate mechanisms, in order of speed:

1. **Progress** is written every two seconds, so the dashboard shows the stall
   within seconds.
2. **The lease** is five minutes. A `claim` from any node first requeues rows in
   `encoding`/`verifying`/`replacing` whose `updated_at` is older than the lease.
   A dead node's file goes back to the queue without a human.
3. **The journal** (`node.journal`, next to the config) is written before work
   starts. If the process was killed outright, the next start reports
   `E_NODE_INTERRUPTED` against that file, which counts as an attempt. Three
   attempts and the file is parked as `failed` rather than looping.

## Error codes

Quoted in `last_error_code`, in the activity feed, and in the JSON API.

| Code | Means | Who fixes it |
|---|---|---|
| `E_PROBE_FFPROBE` | ffprobe could not read the file | the file (corrupt, zero bytes) |
| `E_ENCODE_FFMPEG` | ffmpeg exited non-zero | usually a bad source |
| `E_ENCODE_NO_OUTPUT` | ffmpeg exited 0 and wrote nothing | look at the stderr tail in `last_error` |
| `E_ENCODE_CANCELLED` | the process was killed | operator |
| `E_VERIFY_DECODE` | the output does not decode | the encoder, or the disk |
| `E_VERIFY_VMAF` | below the quality floor at every rung tried | nothing — see below |
| `E_VERIFY_DURATION` | output is materially shorter than the source | investigate |
| `E_VERIFY_SIZE` | output is implausibly small or large | investigate |
| `E_NODE_INTERRUPTED` | the node died mid-file | nothing; it retries |
| `E_NODE_SERVER` | the orchestrator is unreachable | check the container |
| `E_NODE_AUTH` | bad or missing token | config |
| `E_SOURCE_MISSING` | the file vanished between planning and claiming | rescan |
| `R_QUALITY_FLOOR` | content that cannot be re-encoded without visible loss | nothing — it is skipped by design |

`R_` codes are verdicts, not failures: the file was considered and deliberately
left alone. `E_` codes are things that went wrong.

## Reading state without the UI

    messer-transcode node --once --config …      # take one file and stop
    curl -s localhost:8099/api/v1/status | jq .
    sqlite3 state.db "SELECT state, COUNT(*) FROM files GROUP BY 1;"
    sqlite3 state.db "SELECT code, COUNT(*) FROM events GROUP BY 1 ORDER BY 2 DESC;"

## Restarting after a config change

Config is read at startup by both processes. Change it, then:

    pm2 restart transcode-node
    docker compose restart transcode     # or the Komodo stack restart

The node does not hot-reload: a file claimed under the old ladder finishes under
the old ladder, which is the least surprising thing it can do.
