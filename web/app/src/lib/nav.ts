import type { Location } from "react-router-dom";

/** Page names as the app says them. The back button's label comes from here. */
export const PAGES = {
  "/": "Overview",
  "/queue": "Queue",
  "/runs": "Runs",
  "/config": "Config",
  "/settings": "Settings",
} as const;

export type PagePath = keyof typeof PAGES;

/** What a detail page records about where it was opened from. */
export type Origin = { to: string; label: string };

/**
 * Where "back" goes, and what it is called.
 *
 * A detail page is reachable from more than one place (a queue filter, the
 * dashboard, a run), so a static label is a lie half the time — it says "Queue"
 * to someone who arrived from Runs. Every link into a detail route therefore
 * passes `state.from`, and the button names the origin it actually came from.
 * Opening the URL cold has no origin, so the parent route is the honest fallback.
 *
 * The destination is a path, not `history.back()`: the label and the target must
 * never disagree, and a page loaded from a bookmark has no history to pop.
 */
export function resolveBack(state: unknown, parent: PagePath): Origin {
  const from = (state as { from?: Partial<Origin> } | null)?.from;
  if (from && typeof from.to === "string" && from.to.startsWith("/") && typeof from.label === "string" && from.label) {
    return { to: from.to, label: from.label };
  }
  return { to: parent, label: PAGES[parent] };
}

/** The same resolution, without the caller having to know the Location shape. */
export function backTarget(loc: Location, parent: PagePath): Origin {
  return resolveBack(loc.state, parent);
}
