import type { FileQuery } from "./api";
import type { QueueSummary } from "./types";

/**
 * The queue's tabs.
 *
 * Each tab is a *filter the server understands*, not a client-side slice: the
 * queue is 100 rows a page out of ~3k, so "Done" has to be a query or it is a
 * tab that only ever shows the first page of the answer. The counts come from
 * `/api/v1/queue`, which counts states rather than decisions — the old
 * "To encode 761" was every file the planner ever decided to encode, finished
 * ones included, which is why the queue looked like it only grew.
 */
export type QueueTabKey =
  | "queued"
  | "encode"
  | "remux"
  | "inflight"
  | "failed"
  | "done"
  | "skipped"
  | "gone"
  | "everything";

export type QueueTab = {
  key: QueueTabKey;
  label: string;
  count: (c: QueueSummary) => number;
  query: Pick<FileQuery, "state" | "state_in" | "decision" | "sort">;
};

export const TABS: QueueTab[] = [
  { key: "queued", label: "Queued", count: (c) => c.queued, query: { state: "planned" } },
  { key: "encode", label: "Encode", count: (c) => c.encode, query: { state: "planned", decision: "encode" } },
  { key: "remux", label: "Remux", count: (c) => c.remux, query: { state: "planned", decision: "remux" } },
  {
    key: "inflight",
    label: "Running",
    count: (c) => c.inflight,
    query: { state_in: "encoding,verifying,replacing" },
  },
  { key: "failed", label: "Failed", count: (c) => c.failed, query: { state: "failed" } },
  { key: "done", label: "Done", count: (c) => c.done, query: { state: "done", sort: "recent" } },
  { key: "skipped", label: "Skipped", count: (c) => c.skipped, query: { state: "skipped" } },
  // Retired, not broken: the scan did not find these files. Its own tab because
  // "why is the library 10 TB when the disk says 7" has to have an answer.
  { key: "gone", label: "Gone", count: (c) => c.gone, query: { state: "gone" } },
  { key: "everything", label: "All", count: (c) => c.everything, query: {} },
];

export function tabByKey(key: string | null): QueueTab {
  return TABS.find((t) => t.key === key) ?? TABS[0];
}

/** The tab the URL describes. `tab` is the source of truth; the old
 * `?state=&decision=` links still resolve, because they were the previous
 * release's shape and a bookmark should not land on the wrong list. */
export function tabFrom(params: URLSearchParams): QueueTab {
  const named = params.get("tab");
  if (named) return tabByKey(named);
  const state = params.get("state") ?? "";
  const stateIn = params.get("state_in") ?? "";
  const decision = params.get("decision") ?? "";
  if (!state && !stateIn && !decision) return TABS[0];
  return (
    TABS.find(
      (t) =>
        (t.query.state ?? "") === state &&
        (t.query.state_in ?? "") === stateIn &&
        (t.query.decision ?? "") === decision,
    ) ?? TABS[TABS.length - 1]
  );
}

export type Column = "file" | "state" | "why" | "source" | "target" | "size" | "finished" | "saved" | "vmaf";

/**
 * Columns per tab. A finished row's interesting numbers are what it saved and
 * what quality it reached; a queued row's are the source and the target. One
 * table, two shapes — the alternative is columns that are empty on every tab
 * that does not use them.
 */
export function columnsFor(tab: QueueTabKey): Column[] {
  switch (tab) {
    case "done":
      return ["file", "state", "finished", "saved", "vmaf", "size"];
    case "skipped":
    case "gone":
      return ["file", "state", "why", "size"];
    case "inflight":
    case "failed":
      return ["file", "state", "why", "source", "target", "size"];
    default:
      return ["file", "state", "why", "source", "target", "size"];
  }
}

/** The link a file row should return to: this tab, this page, this search. */
export function queueHref(tabKey: string, page: number, q: string): string {
  const p = new URLSearchParams();
  if (tabKey !== TABS[0].key) p.set("tab", tabKey);
  if (page > 1) p.set("page", String(page));
  if (q) p.set("q", q);
  const s = p.toString();
  return "/queue" + (s ? `?${s}` : "");
}
