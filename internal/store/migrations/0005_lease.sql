-- 0005_lease.sql — the lease counts heartbeats, not the claim.
--
-- ReapExpired gives a row back when its node has gone silent, and it read
-- `updated_at`. But the node never wrote that column after claiming: the
-- progress heartbeat (SetProgress) touched only `progress` and `speed`. So the
-- lease really meant "five minutes since the claim", and every file that took
-- longer than that was declared abandoned while it was being encoded.
--
-- The damage was invisible in the state machine and expensive in the window:
-- the prefetched row waits for a whole encode before the encoder takes it, so
-- it was reaped and handed out again on every file; the node then re-ingested
-- and re-encoded work it had already done, and the duplicate died at verify as
-- `E_SOURCE_CHANGED` or was rejected as `(state done)`. Measured 2026-11-13..16:
-- 9–13 duplicate claims a night, 1.5–2.5 h of a 5-h window.
--
-- `updated_at` keeps its other meaning (the scanner stamps it, the dashboard
-- reads it as "since"), so the lease gets its own column rather than
-- overloading a timestamp that four other queries depend on.

ALTER TABLE files ADD COLUMN lease_at INTEGER NOT NULL DEFAULT 0;
