# Configuration reference

`config.json` is the whole behaviour of the tool. It is validated on load: unknown keys are
errors, not warnings, because a silently ignored typo is the difference between "encode one tier"
and "encode everything".

```sh
messer-transcode validate config.json     # every problem at once, by path
messer-transcode schema                   # the JSON Schema, for editors
```

## Where it lives

Operationally: `~/Server/configs/video/transcode/config.json`, alongside the other service configs.
In the repo: `config.example.json` is the shipped, validated example. Real configs are gitignored
because they contain host paths.

## `libraries`

One entry per root the node walks. Roots must be absolute and may not be `/`.

```json
{ "name": "movies",
  "root": "/Volumes/Media/Library/Movies",
  "extensions": [".mkv", ".mp4", ".m4v", ".mov"],
  "exclude": ["**/Sample/**", "**/*.sample.mkv", "**/.*", "**/Extras/**"] }
```

`exclude` supports three glob constructs: `**` (any number of directories, including none),
`*` (within one path segment), `?` (one character). A directory that matches is not descended
into, so an excluded `Sample/` costs nothing to skip.

Hidden files and directories (`.something`) are always skipped.

## `schedule`

```json
{ "weekday": "00:30-05:30", "weekend": "02:00-07:00",
  "drain_before_minutes": 15, "timezone": "Europe/Copenhagen" }
```

Windows are `HH:MM-HH:MM` and may wrap past midnight (`23:00-03:00`). `drain_before_minutes` is
how long before the window closes the server stops handing out new work so the in-flight file can
finish; a window shorter than the drain is a validation error.

## `workers`

```json
{ "encode": 1, "probe": 4, "ingest_buffer": 2 }
```

`encode` is 1 by default and should stay 1: the machine has one media engine, and measurements
showed 1, 2 and 4 simultaneous encodes produce identical aggregate throughput. `probe` may be
raised — probing is I/O-bound, not compute-bound. `ingest_buffer` is how many files may sit
pre-staged on fast storage ahead of the encoder.

## `paths`

```json
{ "scratch": "/Volumes/Active/Hot/transcode", "quarantine": "/Volumes/Media/Quarantine" }
```

Both absolute, both on the node. Validation rejects a scratch or quarantine directory that lives
inside a library root — otherwise the scanner would ingest its own temporary files, and
quarantined originals would be picked up again.

## `quality`

```json
{ "mode": "table", "vmaf_target": 95.0, "vmaf_tolerance": 1.0,
  "table": [
    { "codec": "h264", "height_gte": 2000, "kbps": 14000 },
    { "codec": "h264", "height_gte": 1000, "kbps": 5500 }
  ],
  "probe_outliers_above_kbps": 12000,
  "min_saving_pct": 10 }
```

`mode` is `table` (use the ladder), `probe` (search the ladder per file for outliers) or `fixed`
(testing only).

The table is matched **most-specific-first**: the first row whose codec matches and whose
`height_gte` the file clears wins. Rows must therefore be ordered descending by `height_gte`
within each codec, and validation enforces it — otherwise a 4K file would silently get a 1080p
bitrate.

`vmaf_target` is what the ladder was calibrated to. `vmaf_tolerance` is how far below target a
verified output may land before it is rejected.

`min_saving_pct` is the smallest projected reduction worth spending an encode on. Without it, a
file already below the ladder target would be "re-encoded" upward — larger output, hours wasted.

## `audio` and `subtitles`

```json
"audio": {
  "keep_languages": ["eng", "swe", "dan"],
  "keep_original_language": true,
  "max_tracks": 3,
  "drop_commentary": true,
  "transcode": [{ "from": ["truehd", "flac", "pcm_bluray"], "to": "eac3", "kbps": 448 }]
}
```

`keep_original_language` keeps the film's own language even when it is not in `keep_languages` —
a Japanese film keeps its Japanese track. Track selection takes the first `max_tracks` survivors
after commentary is dropped.

Commentary is detected from the track title and the container's `comment` flag. Hearing-impaired
subtitles are detected from `SDH`, `hearing impaired`, `closed caption`, or a standalone `CC`
token — not from a bare substring, so "Picard" does not read as closed captions.

## `rules`

An ordered list; **the first match wins**. Conditions in one rule are ANDed.

```json
{ "when": { "codec": "hevc", "bitrate_kbps_gte": 8000 }, "do": "encode", "code": "R_HEVC_OVERBITRATE" }
```

Condition keys: `height_lt`, `height_gte`, `bitrate_kbps_lt`, `bitrate_kbps_gte`, `codec`,
`default`. `do` is one of `skip`, `remux`, `encode`, `quarantine`.

The last rule must be `{ "when": { "default": true } }`. That is a validation requirement, and it
is what makes "what would you do with this file?" always answerable.

Two guards run after a rule says `encode`:

| Guard | Outcome | Meaning |
|---|---|---|
| target ≥ source bitrate | `R_ALREADY_LEAN` | Re-encoding would grow the file |
| projected saving < `min_saving_pct` | `R_LOW_SAVING` | Not worth an encode |

## `safety`

```json
{ "min_free_gb": 150, "size_ratio_min": 0.35, "size_ratio_max": 0.98,
  "duration_tolerance_s": 2, "keep_quarantine_days": 14,
  "verify_sample_seconds": 60, "verify_vmaf": true, "max_attempts": 3,
  "quarantine": "copy" }
```

`min_free_gb` is the floor on both volumes before a job may start. `size_ratio_*` rejects an
output that is implausibly small or large. `duration_tolerance_s` rejects a truncation.
`verify_vmaf` turns on the quality check. `max_attempts` bounds retries before a file is marked
failed.

`quarantine` is `"copy"` (default when the key is absent) or `"off"`. `copy` preserves every
replaced original in `paths.quarantine/<date>/` for `keep_quarantine_days`. `off` skips the copy:
the swap is still one rename inside the destination directory, so a crash before it leaves the
original untouched, but once it succeeds the replaced bytes are gone. Turn it off when the copy's
I/O is the constraint — it is a full read and write of every replaced file, which on a USB library
is the largest consumer of the fast volume. `keep_quarantine_days` still governs directories that
already exist, so the backlog drains normally after switching.

## `notify`

Optional. `radarr`/`sonarr` are called after a successful replacement so their databases rescan;
`on` selects which events notify.

## Reason codes

Every decision carries a stable code, which is what the UI, the API and any agent read.

| Code | Decision | Meaning |
|---|---|---|
| `R_AUDIO_NEEDS_EAC3` | remux | Lossless or DTS audio; the video is already efficient |
| `R_TOO_MANY_TRACKS` | remux | More audio streams than the track policy keeps |
| `R_HEVC_OVERBITRATE` | encode | HEVC but spending more bits than the ladder allows |
| `R_H264_ENCODE` | encode | H.264 above the threshold |
| `R_H264_LOW_BITRATE` | encode | H.264 below the threshold but still worth converting |
| `R_ALREADY_LEAN` | skip | Below the ladder target; encoding would grow it |
| `R_LOW_SAVING` | skip | Projected saving is under the floor |
| `R_NO_TARGET` | skip | No ladder row for this codec and height |
| `R_NO_RULE` | skip | Nothing matched (the default rule) |

The ladder has rungs at 4K, 1080p, 720p and below, so a 720p H.264 file is re-encoded at 2,700 kbps
rather than left alone: the height is already accounted for by the rung it picks. HEVC below 1080p
has no encode rule and falls to the default skip, because a low-bitrate HEVC file is already the
thing an encode would try to produce.

A remux rewrites the file with the same video stream: it is only worth its I/O when the audio or
subtitle policy actually changes something. The measured case for the old blanket rules: 1,929
files and 5.7 TB of rewriting to recover 96 GB, of which 1,915 had audio the policy would leave
untouched.
