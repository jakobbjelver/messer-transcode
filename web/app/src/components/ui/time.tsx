export type TimeProps = {
  value: Date | string | number;
  mode?: "relative" | "absolute" | "both";
  locale?: string;
  className?: string;
};

const DIVISIONS: Array<[number, Intl.RelativeTimeFormatUnit]> = [
  [60, "second"],
  [60, "minute"],
  [24, "hour"],
  [7, "day"],
  [4.35, "week"],
  [12, "month"],
  [Number.POSITIVE_INFINITY, "year"],
];

function relative(date: Date, locale: string) {
  const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  let delta = (date.getTime() - Date.now()) / 1000;
  for (const [size, unit] of DIVISIONS) {
    if (Math.abs(delta) < size) return formatter.format(Math.round(delta), unit);
    delta /= size;
  }
  return formatter.format(Math.round(delta), "year");
}

/**
 * A timestamp in the machine voice: mono, tabular, with the absolute value in `title` so a relative
 * label never hides the fact (rule §12: absolute in the title, relative in the label).
 */
export function Time({ value, mode = "relative", locale = "en-GB", className }: TimeProps) {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return <span className={className}>{String(value)}</span>;
  }
  const absolute = new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
  const label = mode === "absolute" ? absolute : relative(date, locale);
  return (
    <time
      dateTime={date.toISOString()}
      title={mode === "both" ? undefined : absolute}
      className={[
        "font-mono text-xs tabular-nums text-[var(--text-2)]",
        className ?? "",
      ].join(" ")}
    >
      {label}
    </time>
  );
}
