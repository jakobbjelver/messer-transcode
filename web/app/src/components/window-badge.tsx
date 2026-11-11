import { usePulse } from "@/components/stream-provider";
import { StatusBadge } from "@/components/ui/status-badge";
import { statePresentation } from "@/lib/status";
import type { WindowState } from "@/lib/types";

/**
 * The topbar's one status read: what the machine is doing, else whether the
 * window is open. It reads the shared stream so the shell needs no fetch of its own.
 */
export function WindowBadge() {
  const { pulse } = usePulse();
  const window: WindowState | null = pulse?.window ?? null;
  const live = pulse?.live ?? null;

  if (live) {
    const { status, label } = statePresentation(live.stage);
    return <StatusBadge status={status}>{label}</StatusBadge>;
  }
  if (!window) return null;
  if (window.open) return <StatusBadge status="success">Window open</StatusBadge>;
  return (
    <StatusBadge status="neutral">
      Window shut
      {/* The next-open time is the first thing to go when the bar is tight. */}
      <span className="hidden sm:inline"> · {window.next_at ?? "?"}</span>
    </StatusBadge>
  );
}
