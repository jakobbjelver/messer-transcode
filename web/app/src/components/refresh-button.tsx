import { ArrowsClockwise } from "@phosphor-icons/react";

import { Button } from "@/components/ui/button";

/**
 * The refresh affordance every list page shares.
 *
 * The icon spins while the fetch is in flight — that is the whole point: the
 * button answers "did my click do anything?" without a spinner replacing the
 * table. `motion-reduce` turns it off for anyone who asked for less motion, and
 * `aria-busy` carries the same fact to a screen reader.
 *
 * Deliberately not disabled while busy: disabling dims the button to 45 % and
 * the canon reads that as "unavailable", not "working". A second click is just
 * another refetch.
 */
export function RefreshButton({
  onRefresh,
  busy,
  label = "Refresh",
}: {
  onRefresh: () => void;
  busy: boolean;
  label?: string;
}) {
  return (
    <Button variant="outline" onClick={onRefresh} aria-busy={busy} data-refreshing={busy ? "true" : undefined}>
      <ArrowsClockwise
        weight="bold"
        className={busy ? "animate-spin motion-reduce:animate-none" : undefined}
      />
      {label}
    </Button>
  );
}
