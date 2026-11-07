# Contributing

**Issues are welcome. Pull requests are not.**

That is a deliberate choice, not a policy statement about anyone's work. This
tool is shaped around one library, one machine and one person's idea of what
"transparent" means for their films. A patch that works in someone else's setup
would still have to be kept working against mine, and I would rather spend that
time on the tool than on reviewing diffs I cannot test.

## What is genuinely useful

- **A bug report that reproduces.** Include:
  - `messer-transcode doctor --json` (it redacts paths? it does not — read it
    before pasting if that matters to you)
  - the failing `E_*` or `R_*` code and the log lines around it
  - what you expected versus what happened
- **A calibration result from different hardware.** The bitrate ladder in
  `docs/calibration.md` was measured on an M4 Pro. An M1, M2 or M3 curve would
  be a real contribution to the documentation.
- **A decision the rules get wrong.** Point at a file, its `ffprobe` output and
  the rule code it received. The rule list is small and arguable.

## What will be closed without discussion

- "Please add GPU support for X" — the whole design assumes one hardware
  encoder in one place.
- Pull requests, however good.
- Feature requests that amount to "be more like Tdarr". Tdarr is documented in
  `docs/history-tdarr.md` for a reason.

## If you want to run it

It is MIT licensed and nothing stops you from forking it, which is the honest
answer to "but I want to change something". Take the ideas, leave the shape.
