import type { ReactNode } from "react";

export type EmptyStateProps = {
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  variant?: "panel" | "bare";
  className?: string;
};

/**
 * What this is, why it is empty, one action (rule §12). The icon is decorative; the title carries
 * the meaning, so nothing here depends on colour.
 */
export function EmptyState({
  icon,
  title,
  description,
  action,
  variant = "panel",
  className,
}: EmptyStateProps) {
  return (
    <div
      className={[
        "grid place-items-center gap-2 px-4 py-8 text-center",
        variant === "panel" ? "border border-border bg-[var(--surface)]" : "",
        className ?? "",
      ].join(" ")}
    >
      {icon ? (
        <span className="text-[var(--text-3)] [&_svg]:size-6" aria-hidden="true">
          {icon}
        </span>
      ) : null}
      <p className="m-0 text-sm font-medium text-[var(--foreground)]">{title}</p>
      {description ? (
        <p className="m-0 max-w-[46ch] text-xs leading-relaxed text-[var(--text-3)]">{description}</p>
      ) : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}
