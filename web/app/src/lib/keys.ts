import type { FileQuery } from "./api";

/**
 * Every cache key in the app, in one place.
 *
 * Two rules keep this honest:
 *
 * 1. A key is built here, never inline at a call site — so "invalidate the
 *    queue" is one line in `stream-provider.tsx` instead of a hunt.
 * 2. Anything built from file rows lives under `files`/`runs`/`run`/`events`,
 *    which is exactly the set the stream's revision invalidates. The live
 *    numbers (status) arrive *inside* the pulse, so they are written, not
 *    invalidated.
 */
export const qk = {
  status: ["status"] as const,
  control: ["control"] as const,
  files: (q: FileQuery) => ["files", q] as const,
  file: (id: number) => ["file", id] as const,
  runs: (last: number) => ["runs", last] as const,
  run: (id: number) => ["run", id] as const,
  events: (limit: number) => ["events", limit] as const,
  config: ["config"] as const,
  configHistory: (limit: number) => ["config", "history", limit] as const,
  health: ["health"] as const,
};

/**
 * Query-key prefixes a new event invalidates. Ordered shortest first; each one
 * is a prefix, so `["files"]` matches every filter and page.
 */
export const REV_INVALIDATED: readonly (readonly string[])[] = [
  ["files"],
  ["runs"],
  ["run"],
  ["events"],
];
