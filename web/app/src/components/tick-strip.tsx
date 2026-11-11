import { cn } from "@/lib/utils";
import { pct } from "@/lib/format";

const TICKS = 60;

/**
 * The queue as sixty ticks, one per 1.67%. It is the dashboard's one figure that
 * reads at a glance without a number, and it is built from utilities rather than
 * a chart library: a div per tick, no canvas, no SVG.
 */
export function TickStrip({
  done,
  encodable,
  running,
}: {
  done: number;
  encodable: number;
  running: boolean;
}) {
  const filled = encodable > 0 ? Math.round(Math.min(1, done / encodable) * TICKS) : 0;
  const share = encodable > 0 ? (done / encodable) * 100 : 0;

  return (
    <div
      role="img"
      aria-label={`${done} of ${encodable} encodable files done, ${pct(share)}`}
      className="flex h-5 items-stretch gap-[3px]"
      data-tick-strip=""
    >
      {Array.from({ length: TICKS }, (_, i) => (
        <span
          key={i}
          data-state={i < filled ? "filled" : i === filled && running ? "live" : "empty"}
          className={cn(
            "min-w-px flex-1",
            i < filled && "bg-foreground",
            i === filled && running && "bg-warning",
            i >= filled && !(i === filled && running) && "bg-[var(--surface-2)]",
          )}
        />
      ))}
    </div>
  );
}
