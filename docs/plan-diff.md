# Plan diff: messer-transcode vs Tdarr

The M1 gate. Every file in the library, decided by the new rule list, compared against what
Tdarr's flow had decided. Run 2026-11-06: 3,069 files scanned, 3,065 probed, 4m22s.

## Headline

Tdarr wanted to process **2,946 of 3,205 files (92 %)**. This rule list wants to re-encode **440
(14 %)**, and remux another 2,015 without touching the video. The three-month estimate was not a
hardware problem — it was the flow asking for 6.7× more encoder work than the library needs.

## The matrix

Tdarr's decision column does not carry the flow's *reason*, so the comparison is at the level of
"what happened next". `filejsondb.transcode_decision_maker` on the left, our verdict on the right.

| Tdarr | Ours | Reason | Files |
|---|---|---|---:|
| Transcode error | encode | `R_H264_ENCODE` | 296 |
| Transcode error | encode | `R_HEVC_OVERBITRATE` | 110 |
| Transcode error | remux | `R_HEVC_EFFICIENT` | 878 |
| Transcode error | remux | `R_LOW_RES` | 864 |
| Transcode error | skip | `R_NO_RULE` | 433 |
| Transcode error | skip | `R_ALREADY_LEAN` | 92 |
| Transcode error | skip | `R_LOW_SAVING` | 69 |
| Transcode error | — | file no longer exists | 204 |
| Transcode success | remux | `R_HEVC_EFFICIENT` | 66 |
| Transcode success | remux | `R_LOW_RES` | 46 |
| Transcode success | — | file no longer exists | 1 |
| Not required | remux | `R_HEVC_EFFICIENT` | 65 |
| Not required | remux | `R_LOW_RES` | 34 |
| Queued | encode | `R_H264_ENCODE` / `R_HEVC_OVERBITRATE` | 15 |
| Queued | remux | `R_LOW_RES` / `R_HEVC_EFFICIENT` | 17 |
| Queued | skip | `R_NO_RULE` / `R_ALREADY_LEAN` / `R_LOW_SAVING` | 10 |

## Verdict per class

**`R_HEVC_EFFICIENT` (878 + 66 + 65 = 1,009).** Disagreement, deliberate. Tdarr's flow ran the
"skip if HEVC under 5 Mbps" test *after* the encode decision had already been taken, so efficient
HEVC was queued anyway. These files need no video work; a remux at most for stream cleanup. This
single row is 1,009 files of pointless encoder time.

**`R_LOW_RES` (864 + 46 + 34 = 944).** Disagreement, deliberate. The flow's `<1080p → skip` node
sat on a branch that 720p files did not always take. 944 files, 4.98 TB, below 1080p — re-encoding
them would cost quality for almost no space.

**`R_NO_RULE` (433 + 5 = 438).** Agreement in outcome, not in path: these are HEVC between 5 and
8 Mbps, which both systems leave alone. Tdarr reached it by accident.

**`R_H264_ENCODE` + `R_HEVC_OVERBITRATE` (406 + 15 = 421).** Agreement. This is the real work:
1.68 TB of H.264 plus 564 GB of over-bitrate HEVC.

**`R_ALREADY_LEAN` / `R_LOW_SAVING` (161).** New categories, and the reason the projection shrank.
Tdarr's flow had no equivalent: it would re-encode a 2 Mbps 1080p H.264 file *upward* to its
target bitrate. Our planner refuses when the target meets the source, or when the projected saving
is under 10 %.

**204 + 1 "Transcode error" rows with no file on disk.** Tdarr's database references media that is
gone. Not a rule disagreement — a stale-state problem the new schema avoids, because files are
reconciled from the filesystem on every scan.

## What this run found that was not a decision at all

**Four films are zero bytes.**

```
Hereditary (2018)                   0 bytes
Looper (2012)                       0 bytes
Spectre (2015)                      0 bytes
The Tragedy of Macbeth (2021)       0 bytes
```

`ffprobe` reports `EBML header parsing failed` because there is no header, or anything else. These
are re-downloadable, but they are also precisely the failure the seven safety invariants exist to
prevent: verify the replacement before the original moves, and the original is never deleted, only
quarantined.

**Tdarr indexed macOS AppleDouble files.** Five `._<name>.mkv` resource-fork stubs sit in its
database as media. They are invisible to the new scanner by design (hidden files are skipped), and
they are not counted in the matrix above.

## Projection

| | Files | Input | Output | Reclaimed |
|---|---:|---:|---:|---:|
| Encode | 440 | 2.24 TB | 1.46 TB | **780 GB** |
| Remux (streams only) | 2,015 | 6.16 TB | 6.16 TB | — |
| Skip | 610 | 1.76 TB | 1.76 TB | — |
| Unreadable | 4 | 0 | 0 | — |

Encoder time: ≈830 hours of content at 8.6–11.8× realtime → **≈80 encode-hours**, or about
17 nights at the current 5-hour window.

The earlier 1.6 TB estimate came from Tdarr's own bitrate column, which encoded what the flow
*intended* rather than what the files contain. This number comes from `ffprobe` on the files
themselves.

## Changes made to the design because of this run

1. `R_ALREADY_LEAN` — never encode when the ladder target meets or exceeds the source bitrate.
2. `R_LOW_SAVING` — never encode for a projected saving under `min_saving_pct` (default 10 %).
3. Zero-byte files are recorded as `E_PROBE_FFPROBE` failures in state rather than crashing the
   run, so they surface in the UI and the health check instead of disappearing.
