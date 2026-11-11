import { ArrowLeft } from "@phosphor-icons/react";
import { Link, useLocation } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { backTarget, type PagePath } from "@/lib/nav";

/**
 * "Back to <where you came from>" as a page-level link.
 *
 * It sits above the page header, at the left edge of the content, because that
 * is where a person looks for it: the header is about this page, and the way
 * out of it belongs before it, not beside the title. Never a static word — the
 * label and the destination both come from the link that opened this page (see
 * `lib/nav.ts`), falling back to `parent` when the URL was opened cold.
 */
export function PageBack({ parent, className }: { parent: PagePath; className?: string }) {
  const loc = useLocation();
  const target = backTarget(loc, parent);
  return (
    <Link
      to={target.to}
      className={[
        "inline-flex w-fit items-center gap-1.5 text-sm text-[var(--text-2)]",
        "transition-colors hover:text-[var(--foreground)] hover:underline hover:underline-offset-4",
        className ?? "",
      ]
        .join(" ")
        .trim()}
    >
      <ArrowLeft size={14} weight="bold" />
      Back to {target.label}
    </Link>
  );
}

/** The same target as a button, for an error page's action row. */
export function BackButton({ parent }: { parent: PagePath }) {
  const loc = useLocation();
  const target = backTarget(loc, parent);
  return (
    <Button variant="outline" asChild>
      <Link to={target.to}>
        <ArrowLeft weight="bold" />
        Back to {target.label}
      </Link>
    </Button>
  );
}
