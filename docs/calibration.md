# Calibration — what this encoder actually delivers

`messer-transcode calibrate` measured the quality/bitrate curve on this library on 2026-11-06.
Method: three encode candidates per codec, two 60-second windows each, encoded at five bitrates with
the production settings, scored against the source with libvmaf. 60 measurements, 26m17s.

Samples: `10 Things I Hate About You` (10.5 Mbps), `Psycho` (8.2 Mbps), `Line of Duty S06E01`
(7.2 Mbps) for H.264; `A Complete Unknown` (8.7 Mbps), `Better Call Saul S01E10` (8.5 Mbps),
`Line of Duty S06E05` (8.5 Mbps) for HEVC.

## The curves

**HEVC sources → HEVC**

| kbps | mean VMAF | worst window |
|---:|---:|---:|
| 4000 | 93.9 | 90.5 |
| 5000 | 94.5 | 90.8 |
| **6000** | **95.0** | 91.3 |
| 7000 | 95.2 | 91.3 |
| 8500 | 95.6 | 91.6 |

**H.264 sources → HEVC**

| kbps | mean VMAF | worst window |
|---:|---:|---:|
| 4000 | 91.2 | 82.0 |
| 5000 | 92.2 | 83.4 |
| **6000** | **92.9** | 84.7 |
| 7000 | 93.4 | 85.7 |
| 8500 | 94.1 | 87.1 |

## Finding 1 — the HEVC rung was right for the wrong reason

6,000 kbps lands exactly on 95.0 for HEVC sources. That was the number in the plan from a single
film measured during the investigation; it survives a proper sample.

## Finding 2 — H.264 has a ceiling, and bitrate cannot lift it

Doubling the bitrate from 4,000 to 8,500 buys **2.9 VMAF** and costs a file that is larger than the
one it replaces for a third of the library. The curve is nearly flat because the limit is not
bitrate: `Psycho` (1960, black and white, heavy grain) loses its grain at any rung, and VMAF punishes
that. `R_LOW_RES` and `R_ALREADY_LEAN` are the only honest responses to most of this tier.

The consequence: **a single global target of 95 is unattainable for H.264 sources**, so holding every
encode to it would either fail every H.264 job or force pointless bitrates. Ladder rows now carry
`expected_vmaf` — the quality calibration measured for that rung — and verification holds each file
to 92.9 or 95.0 depending on which tier it came from.

## Finding 3 — the spread is the real problem

At 6,000 kbps the H.264 worst window is 84.7 while the mean is 92.9. A single rung is too generous
for clean animation and too aggressive for `Psycho`.

The first real encode after calibration showed how bad this is: `Friends S02E16`, a grainy Bluray
HEVC at 10.1 Mbps, scored **90.8** at 6,000 kbps and **92.5** even at 8,400 — against a 95
expectation. Grain does not survive a re-encode at any bitrate, so no amount of escalation reaches
the floor on that file.

Two mechanisms now handle it:

1. **Two-window verification.** A single 60-second sample moves several points on grain-heavy
   content, so the quality check averages windows at 40 % and 70 % of the duration.
2. **One escalation, then leave it alone.** A quality miss re-encodes once at 1.4× the target.
   If that also misses, the file is **skipped with `R_QUALITY_FLOOR`** — not failed, not retried
   forever. The original is untouched and the UI says why.

That last point matters more than it looks: a file whose content cannot be re-encoded without
visible loss is not an error condition, it is the correct verdict. The projection of 671 GB is
therefore an upper bound, and a few percent of the H.264 tier will land in `R_QUALITY_FLOOR`.

## Applied table

```json
{ "codec": "h264", "height_gte": 1000, "kbps": 6000, "expected_vmaf": 92.9 },
{ "codec": "hevc", "height_gte": 1000, "kbps": 6000, "expected_vmaf": 95.0 }
```

Rungs above and below 1080p are scaled (4K ×2.6, 720p ×0.45, SD ×0.16) and marked extrapolated in
the command's output. The library has no 4K today, so those rows are unused.

Re-planning the whole library with the calibrated table moves the projection from 780 GB to **671 GB**:
the H.264 target rose from 5,500 to 6,000 and 74 more files fell below the 10 % saving floor. That is
the cost of using a measured number instead of an assumed one, and it is the right trade — 671 GB
with a quality promise per file beats 780 GB with a guess.

## Reproducing

```sh
messer-transcode calibrate --config config.json --db state.db \
  --target 95 --per-tier 3 --windows 2 --window 60 --rungs 4000,5000,6000,7000,8500
```

Re-run it when the encoder, the macOS version or the content mix changes materially. The numbers are
a property of this hardware and this library, not of the software.
