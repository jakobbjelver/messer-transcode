import { ArrowLeft } from "@phosphor-icons/react";
import { Link, useLocation } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { backTarget, type PagePath } from "@/lib/nav";

/**
 * "Back to <where you came from>".
 *
 * Never a static word: the label and the destination both come from the link
 * that opened this page (see `lib/nav.ts`), falling back to `parent` when the
 * URL was opened cold.
 */
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
