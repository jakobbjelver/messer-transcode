import type { ReactNode } from "react";

export type StatProps = {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  trend?: { value: ReactNode; tone?: "success" | "warning" | "danger"; icon?: ReactNode };
  className?: string;
};

const toneClass = {
  success: "text-[var(--success)]",
  warning: "text-[var(--warning)]",
  danger: "text-[var(--destructive)]",
} as const;

/**
 * One number with its label. The label is meta (mono caps), the value is data (mono, tabular), and
 * a trend is a status: icon + value, colour reinforcing only (rule 8).
 */
export function Stat({ label, value, hint, trend, className }: StatProps) {
  return (
    <div className={["flex flex-col gap-1", className ?? ""].join(" ")}>
      <span className="font-mono text-[10.5px] uppercase leading-none tracking-[0.1em] text-[var(--text-3)]">
        {label}
      </span>
      <span className="font-mono text-2xl leading-none tabular-nums text-[var(--foreground)]">{value}</span>
      {trend || hint ? (
        <span className="flex flex-wrap items-center gap-1.5 text-xs text-[var(--text-3)]">
          {trend ? (
            <span className={["inline-flex items-center gap-1 font-medium", toneClass[trend.tone ?? "success"]].join(" ")}>
              {trend.icon ? (
                <span className="[&_svg]:size-3.5" aria-hidden="true">
                  {trend.icon}
                </span>
              ) : null}
              {trend.value}
            </span>
          ) : null}
          {hint ? <span>{hint}</span> : null}
        </span>
      ) : null}
    </div>
  );
}
