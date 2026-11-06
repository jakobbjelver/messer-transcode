# M2 rehearsal — evidence

The write path, exercised only against copies. The real library was read once to create those
copies and never written to; its file size is byte-identical before and after
(1,375,233,590 bytes).

## Run 1 — 20 synthetic files

Twelve H.264 clips, four efficient HEVC, two 720p, two already-lean. Built in
`/Volumes/Active/tc-test/`.

| Decision | Files | Result |
|---|---:|---|
| encode (`R_H264_ENCODE`) | 12 | 77.18 MB → 49.69 MB |
| remux (`R_HEVC_EFFICIENT`) | 4 | video copied, streams cleaned |
| remux (`R_LOW_RES`) | 2 | video copied, streams cleaned |
| skip (`R_ALREADY_LEAN`) | 2 | untouched |

**26.6 MB reclaimed against a 27.49 MB projection.** Eighteen files reached `done`, eighteen
originals sit in `quarantine/2026-11-06/`, scratch is empty apart from lock files.

An encoded output: `codec_name=hevc height=1080 bit_rate=5585211` — the 5500 kbps target plus
container overhead, as expected.

## Run 2 — a real episode

`Friends - S02E22`, HEVC 1080p at 8,059 kbps, 1.38 GB, 22.75 minutes. Decided
`R_HEVC_OVERBITRATE` → 6,000 kbps.

| | Before | After |
|---|---:|---:|
| Size | 1.375 GB | 1.131 GB |
| Bitrate | 8,059 kbps | 6,625 kbps |
| Duration | 1,365.07 s | 1,365.07 s |

Streams afterwards: HEVC video, one `eac3` English audio track, subtitles `eng (MAX)`, `dan`,
`swe`. The English SDH track was dropped, exactly as the policy says, and the Danish and Swedish
tracks survived because they are in `keep_languages`.

The quarantine copy is 1,375,233,590 bytes — the original, intact.

## VMAF against the source

Sixty seconds from 05:00, comparing the quarantined original with the output:

```
mean 94   harmonic 94   min 85   max 100
```

The target is 95 with a tolerance of 1, so this passes at the boundary. Two things follow:

1. **The 6,000 kbps rung is marginal, not comfortable.** M6's calibration should test 6,500 and
   7,000 on a dozen real files before the first unattended night.
2. **This is a second-generation measurement.** The source was already an HEVC encode, so VMAF
   here means "nearly indistinguishable from the existing copy", not "from a disc master". A
   library-wide calibration must sample H.264 sources too, where the comparison is kinder.

## What the gate did not cover

`vmaf` in the database is still 0: verification records duration, size ratio and a four-window
decode, and VMAF scoring lands with M6's calibration work. The number above was measured by hand,
not by the pipeline — do not read it as a pipeline output.
