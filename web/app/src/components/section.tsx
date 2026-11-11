import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/**
 * A page block: hairline on top, a mono meta eyebrow, an optional right-hand
 * note. The old slate numbered these [01]…[06] and captioned them FIG. n; the
 * canon puts meta in the mono voice and leaves the headings to Golos, so the
 * eyebrow carries the label and no caption is printed.
 */
export function Section({
  eyebrow,
  aside,
  children,
  className,
}: {
  eyebrow: string;
  aside?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("border-t border-border pt-4", className)}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 pb-3">
        <span className="m-label">{eyebrow}</span>
        {aside ? <span className="m-label">{aside}</span> : null}
      </div>
      {children}
    </section>
  );
}
