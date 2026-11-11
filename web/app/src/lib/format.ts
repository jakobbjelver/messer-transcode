// Byte and time formatting, mirroring internal/ui's old template helpers so the
// numbers a page shows do not change meaning with the port.

export function bytes(n: number): string {
  if (!Number.isFinite(n)) return "—";
  if (n >= 1e12) return (n / 1e12).toFixed(2) + " TB";
  if (n >= 1e9) return (n / 1e9).toFixed(2) + " GB";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + " MB";
  if (n >= 1e3) return (n / 1e3).toFixed(0) + " KB";
  return n + " B";
}

/** Gigabytes with one decimal: the dashboard's unit of account. */
export function gb(n: number): string {
  if (!Number.isFinite(n)) return "—";
  return (n / 1e9).toFixed(1);
}

export function kbps(n: number): string {
  return n > 0 ? n.toLocaleString("en-GB") + " kbps" : "—";
}

export function duration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor(seconds / 60) % 60;
  const s = Math.floor(seconds) % 60;
  if (h > 0) return `${h}h ${String(m).padStart(2, "0")}m`;
  if (m > 0) return `${m}m ${String(s).padStart(2, "0")}s`;
  return `${s}s`;
}

export function pct(v: number, digits = 0): string {
  if (!Number.isFinite(v)) return "—";
  return v.toFixed(digits) + "%";
}

/** "14:32" from a unix second count; "—" when the server sent zero. */
export function clock(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
}

/** "Mon 02 Jan" from an ISO timestamp. */
export function day(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleDateString("en-GB", { weekday: "short", day: "2-digit", month: "short" });
}

/** "14:32" from an ISO timestamp. */
export function timeOfDay(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
}

/** The last path segment, which is what a table row has room for. */
export function fileName(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] || path;
}
