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
