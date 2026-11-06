# messer-transcode

A transcode pipeline that runs itself. One Go binary, two roles: a containerised
orchestrator that owns the queue and the decisions, and a native macOS worker that
re-encodes video with Apple's VideoToolbox hardware encoder.

**Status: under construction.** The skeleton, configuration contract and CI are in
place; scanning, planning and encoding land milestone by milestone. `docs/PLAN.md`
is the specification and the progress record.

## Why

Tdarr ran here for a year and stopped producing anything in May 2026 — not because of
a bad decision, but because a broken `mkvtoolnix` made it fail *after* burning the
encode, and nothing checked whether a job had actually produced a smaller file. The
replacement is smaller by design:

- **A flat, ordered rule list** instead of a 39-node visual flow.
- **One media engine, respected.** Concurrency buys no throughput on this hardware.
- **A measured quality target.** VMAF against your own library, calibrated once.
- **Nothing is ever deleted.** A verified replacement lands, the original moves to a
  dated quarantine.
- **Machine-readable by default.** Stable error codes, `--json` everywhere, a
  `doctor` command that returns a verdict instead of a paragraph.

## Quick start

```sh
go build -o bin/messer-transcode ./cmd/messer-transcode
./bin/messer-transcode version
./bin/messer-transcode validate config.example.json
```

## Configuration

`config.example.json` is a complete, validated example: libraries, schedule window,
quality ladder, audio and subtitle rules, decision rules, and the safety floors.
`messer-transcode schema` prints the JSON Schema; `validate` reports every problem at
once, addressed by path.

## Documentation

| Document | Contents |
|---|---|
| `docs/PLAN.md` | The specification, measured facts and milestone gates |
| `docs/architecture.md` | Processes, state model, safety invariants |

## Licence

MIT — see `LICENSE`.
