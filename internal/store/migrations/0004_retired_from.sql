-- 0004_retired_from.sql — a retired row remembers what it was.
--
-- The catalogue now has a tombstone: a scan retires the rows it did not find, so
-- a deleted file stops being counted in the library and stops being work. But
-- history is a fact, not a state — a run's failures and skips are what the files
-- did that night. Overwriting `state` with 'gone' would erase the failure a row
-- recorded from a night that already happened, which is the same class of bug
-- as re-deciding a file and watching a finished run shrink.
--
-- So the state a retirement replaced is kept beside it, and the run ledger reads
-- `retired_from` when it is set. Nothing else changes: the row is still `gone`
-- for the queue, the dashboard and the plan.

ALTER TABLE files ADD COLUMN retired_from TEXT;
