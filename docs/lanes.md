# The lanes

The encoder is the only scarce resource in this system: one media engine, about
eight to nine times realtime, and nothing else can use it while it works. So
everything that is not encoding happens somewhere else.

```
   claim ──► ingest (prefetch)  ──┐
                                   ├─► encode ──► verify ──┐
   previous output ──► place (egress) ◄────────────────────┘
```

## Why the stages are separate

`Process` used to do it all in one call: copy, encode, verify, swap. That is a
fine shape for the CLI, where you are watching one file, and a bad shape for a
nightly worker, because two of those four steps are I/O the media engine does
not need to wait for.

- **Ingest** copies the source from the library volume to scratch. Two
  gigabytes at 44 MB/s is most of a minute of the encoder doing nothing.
- **Place** copies the finished file back and renames it into position. On the
  real system that is scratch on NVMe to the library on USB: thirty-odd seconds.

Both are now off the critical path:

- the **prefetcher** claims the next file and stages it while the current one
  encodes;
- **egress** places the finished file while the next one encodes.

`transcode.Split` is the seam: it does everything up to and including
verification and returns the swap as a function. `transcode.Commit` runs it.
`transcode.Process` is still there for the CLI and is just those two calls.

## What was measured

Four 35-minute episodes, one node, lanes on:

| | |
|---|---|
| working window (first heartbeat → last placement) | 16 m 54 s |
| encoding | 16 m 57 s |
| **encoder idle** | **0.2 %** |
| placement, off the critical path | 5.5 s total |

Two files, the confirmation run: 8 m 34 s window, 8 m 33 s encoding, 0.2 % idle.

Every job now carries its own numbers in `events` with code `METRICS`:
`{"encode_s":…,"egress_s":…,"wait_s":…}`. `wait_s` is the gap between the
previous encode ending and this one starting — it is 0.0003 s when the prefetch
worked, and it is the number to watch if idle time ever reappears.

### Two things that made the first measurement wrong

**The idle figure counted the wait for work.** The first summary reported 5.7 %
and then 10.3 % for the same code, because the clock ran until shutdown instead
of until the last placement. Waiting for the queue to produce another file is
not time the lanes could have recovered. The clock now stops at the last
placement.

**Verification was synchronous and slow.** Two VMAF windows, one after the
other, was eleven seconds of idle per file. They measure the same encode
independently, so they now run together; that alone was most of the remaining
gap.

## The cost

Prefetching means claiming a file before you are ready to encode it, so the node
holds a claim it is not working on. Two consequences, both handled:

- the claim would expire under the five-minute lease, so the prefetcher
  heartbeats the waiting job every thirty seconds until the encoder takes it;
- on shutdown the file is handed back with `state=planned` rather than left
  claimed, so a stop costs nothing.

The journal became a directory for the same reason: with placement off the
encode path there can be two files in flight, and a crash has to account for
both.

## Deliberately not done

**Placement is not batched.** The plan called for batching copies back to the
library; a queue of one is what actually happens at this throughput, and a batch
would only add latency to the rename that frees the original.

**Ingest does not hash whole files.** The head and tail megabyte each are
enough to catch truncation and a partial copy, which is what an interrupted copy
produces. Hashing 40 GB would take longer than the copy and defeat the point of
staging.
