import type { ReactNode } from "react";

/**
 * The page's measure (canon §16). Every screen renders exactly one PageContainer and passes the same
 * stop to AppShell, so the topbar's inner row lines up with the content column.
 *
 * Stops come from the canon tokens — never hardcode a width in a page again.
 *   default → --shell (1152)   app screens and dashboards
 *   narrow  → --shell-narrow (896)   single-column reading and forms
 *   wide    → --shell-wide (1440)   data tables and boards only
 */

const CONTAINER = {
  default: "max-w-[var(--shell)]",
  narrow: "max-w-[var(--shell-narrow)]",
  wide: "max-w-[var(--shell-wide)]",
} as const;

export type ContainerStop = keyof typeof CONTAINER;

export function PageContainer({
  size = "default",
  className,
  children,
}: {
  size?: ContainerStop;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      data-page-container={size}
      className={`mx-auto w-full px-4 md:px-8 ${CONTAINER[size]} ${className ?? ""}`.trim()}
    >
      {children}
    </div>
  );
}
