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

`schedule.weekday` / `schedule.weekend` are `HH:MM-HH:MM` in
`schedule.timezone`. Outside the window the server answers claims with
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
service. It mounts `/config` — `configs/transcode` — and nothing else. It has no
access to the media volume, which is the point: a component that cannot touch
the library cannot damage it.

The image is built on this host, not pulled:

```bash
cd ~/Repos/messer-transcode
make build   # the binary the node runs
make image   # the container, tagged messer-transcode:<version>
```

`pull_policy: never` in the compose file says so explicitly. Publishing to GHCR
needs a token with `write:packages`; until one is configured the image lives only
on this machine, and `make image` after `make build` is the update path.

**The node** runs under PM2, because VideoToolbox is not reachable from a Linux
container:

```bash
pm2 start deploy/ecosystem.transcode.config.cjs   # first time, from the repo
pm2 restart transcode-node                        # after make build
pm2 logs transcode-node --lines 40 --nostream
```

**Where the state lives.** On a Docker volume (`/data`), not on the bind mount.
SQLite over virtiofs does not work: through the bind mount, 40 of 40 event
writes returned IO errors and `quick_check` reported corrupt pages, while the
host read the same file without complaint. A named volume is a real filesystem
and the errors disappear. Config stays read-only on the bind mount; only the
queue is on the volume.

The consequence to know: the host can no longer read the live queue. `doctor
--role server` against a path is now a check of a copy, not the original — the
authoritative answer for the server half is `GET /api/v1/health`, which is what
`transcode-health.sh` reads.

**The token.** The node authenticates with a token that must not sit in
`config.json` (committed beside it) or in the stack environment (Komodo rewrites
`.env` from its own stack config on every deploy, and writing that array
replaces every other variable in it). It is `/config/server.token`, mode 0600,
gitignored, and read with `--token-file`:

```bash
openssl rand -hex 32 > ~/Server/configs/transcode/server.token
chmod 600 ~/Server/configs/transcode/server.token
```

Rotating it means regenerating the file and restarting both halves.

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
