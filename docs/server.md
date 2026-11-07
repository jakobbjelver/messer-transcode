# The server

`messer-transcode serve` runs the orchestrator: the read API, the film-slate
pages, and the endpoints a node claims work through. It never touches the media
volume. Everything it knows came from a node, which is why it can live in a
container on a machine that cannot see `/Volumes` at all.

```
messer-transcode serve --config ~/Server/configs/video/transcode/config.json
                        --db /path/state.db --listen :8099
```

The database is the whole state. Two servers pointed at the same file would
fight, so run one; the node is the thing you scale.

## Routes

| Method | Path | Purpose |
|---|---|---|
| GET | `/` | overview: reclaim, counts, queue strip, current job, activity |
| GET | `/queue` | the queue, filterable by `state` and `decision`, 100 per page |
| GET | `/files/{id}` | one file: decision, streams, sizes, verification |
| GET | `/settings` | build metadata and the config with the token redacted |
| GET | `/events` | server-sent events, one small JSON object per change |
| GET | `/healthz` | `ok`, for a container healthcheck |
| GET | `/api/v1/status` | version, window, dashboard snapshot, live job |
| GET | `/api/v1/live` | just the job in flight |
| GET | `/api/v1/files` | `?state=&decision=&limit=&offset=` |
| GET | `/api/v1/files/{id}` | one file, full detail |
| GET | `/api/v1/events` | `?limit=`, newest first |
| GET | `/api/v1/runs` | `?last=N` — one row per occurrence of the configured window |
| GET | `/api/v1/health` | the verdict object; `503` when broken |
| POST | `/api/v1/node/register` | handshake, returns server time |
| POST | `/api/v1/node/claim` | claim one job, or `204` when there is none |
| POST | `/api/v1/node/jobs/{id}/heartbeat` | `{"fraction":0.42,"speed":8.4}` |
| POST | `/api/v1/node/jobs/{id}/result` | `{"state":"done\|skipped\|failed","code":"","detail":""}` |

Every failure is JSON with a stable code: `{"error":{"code":"E_NOT_FOUND","message":"..."}}`.

## The node contract

The node pulls work; the host never accepts an inbound connection. It sends
`X-Node-Token` on every call, and the server refuses all four node endpoints
when `server.token` is unset rather than serving an open claim endpoint.

Claiming is a state transition, not a read: `ClaimWork` moves the file to
`encoding` inside the same transaction, so two nodes cannot take the same file.
`lease_seconds` is 300 — a node that dies mid-job leaves a row in `encoding`,
which `Live()` exposes as stale rather than silently recovered.

Progress lands on the file row (`progress`, `speed`) on every encoder callback
that is more than two seconds old. The server holds no in-memory job state, so
the dashboard shows the truth even when the encode is a different process on a
different machine.

This contract is provisional until M4, which brings the node agent and the
measurements a real result needs (bytes before and after, VMAF, verdict detail).
That changes a private endpoint, not a migration.

## The pages

Film slate: monochrome at rest, one mono family, hairline rules, `[*]` section
numbers, `FIG. n.` captions, sprocket perforations down the left edge. Amber
appears only while a job is running — the header status, the queue strip tick,
the stage word, the progress bar, and a 2px rule on the job box. Nothing else
is ever coloured except failures in red.

The live stream is one `EventSource` per page. The server polls the snapshot
once a second and sends only changed payloads; the page patches a handful of
`[data-live]` elements and re-renders if the job identity changes.

Two rules learned the hard way while building it:

- `data-live` must sit on the element whose *text is the value*. Replacing
  `textContent` wipes child nodes, so a wrapper carrying a unit loses the unit
  a second after load.
- A flex item with no height collapses to zero. The queue strip looked like a
  blank panel until each tick got an explicit height.

## Why the page polls a snapshot instead of subscribing

The encoder is the only thing that produces events, and it is on another
machine. Polling SQLite once a second is cheaper to reason about than an event
bus, cannot lose a message, and is fast enough: 3,069 rows of aggregates in
under a millisecond.
