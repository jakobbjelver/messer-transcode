import type { ReactNode } from "react";

export type PageToolbarProps = {
  left?: ReactNode;
  right?: ReactNode;
  className?: string;
};

/**
 * The row between the page header and the content: search and filters on the left, the page's
 * actions on the right. 32px tall, so it lines up with the default control height (rule 10).
 */
export function PageToolbar({ left, right, className }: PageToolbarProps) {
  return (
    <div
      className={["flex min-h-8 flex-wrap items-center justify-between gap-3", className ?? ""].join(" ")}
    >
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">{left}</div>
      {right ? <div className="flex shrink-0 items-center gap-2">{right}</div> : null}
    </div>
  );
}
