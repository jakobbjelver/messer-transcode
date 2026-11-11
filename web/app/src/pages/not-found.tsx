import { Link } from "react-router-dom";

import { Page } from "@/components/page";
import { ErrorState } from "@/components/ui/error-state";

export function NotFoundPage() {
  return (
    <Page>
      <ErrorState
        code="E_NO_ROUTE"
        title="No such page"
        description="That address is not one of this app's screens."
        actions={
          <Link
            to="/"
            className="border border-border px-3 py-1.5 text-sm text-[var(--foreground)] no-underline hover:bg-[var(--surface-2)]"
          >
            Back to the overview
          </Link>
        }
      />
    </Page>
  );
}
