# Tdarr ran here

Tdarr was the transcoding pipeline for this library from 2025-10 until
2026-11. This file is what survives of it: the tool was removed because it could
not do the job, and the replacement is `messer-transcode`.

## What it was used for

Exactly one thing: re-encode a large H.264/1080p library to HEVC with
VideoToolbox, overnight, in a window before the backup. No plugin ecosystem, no
detailed stats, no library management. That narrow use is why replacing it was
feasible at all.

## Why it was replaced

The investigation on 2026-11-06 found three separate problems, each sufficient
on its own.

**It wanted to re-encode almost the whole library.** Its flow asked for 2,946 of
3,205 files — 92 %. The library does not need that: a rule list with two guards
(a file already at or below the target bitrate, and a file whose projected
saving is under 10 %) asks for 440 files. The "three months to finish" estimate
was flow over-reach, not hardware.

**It had been failing silently for months.** 2,436 files carried
`HealthCheck = Error` and 2,951 carried a transcode error. The cause was
`MKVPropEdit` failing to load `libfmt.11.dylib` after a Homebrew upgrade — and
because every job failed the same way, the daily health check saw "activity" and
stayed green. The tool never asserted that a file came out smaller.

**Its state could not be trusted for a diff.** Five macOS `._` stubs had been
indexed as media, and four films are zero bytes.

## What replaced it

`messer-transcode`: a Go orchestrator in a container plus a native macOS worker,
a measured VMAF ladder instead of per-file bitrate guessing, and a rule list
instead of a flow. The measurements behind it, and the reason a 6,000 kbps HEVC
target and a 92.9 VMAF expectation for H.264 sources are the honest numbers, are
in `docs/calibration.md`.

## What was kept from it

The one idea worth keeping: encode to scratch, verify, then swap — never write
in place. `messer-transcode` does that with a quarantine rather than a delete,
so an original is always recoverable for `safety.keep_quarantine_days`.

## Removal

The container, its compose block, its PM2 app, `configs/video/tdarr/`, its job
reports and its transcode cache were removed in M8, after a shadow week in which
`messer-transcode` planned the same library while Tdarr stayed idle. The archive
of its config is kept with the other dated archives; nothing of it runs here.
