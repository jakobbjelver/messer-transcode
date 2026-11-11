import type { ControlResponse } from "./types";

export type ControlSummary = {
  /** Which badge the strip wears. Colour only reinforces the label (rule 8). */
  tone: "ok" | "paused" | "forced";
  label: string;
  detail: string;
};

/** "2 h 14 m", "14 m", "under a minute" — a duration a person can read. */
export function relative(from: number, to: number): string {
  const secs = Math.max(0, to - from);
  const h = Math.floor(secs / 3600);
  const m = Math.floor(secs / 60) % 60;
  if (h > 0) return m > 0 ? `${h} h ${m} m` : `${h} h`;
  if (m > 0) return `${m} m`;
  return "under a minute";
}

function clock(unix: number): string {
  return new Date(unix * 1000).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
}

/**
 * The control strip's one sentence.
 *
 * The order matters and mirrors the server's: a pause beats an override, an
 * override beats the schedule. A strip that said "window open" while the queue
 * was paused would be the exact kind of lie this release exists to remove.
 */
export function controlSummary(c: ControlResponse | null, encoding: boolean): ControlSummary {
  if (!c) return { tone: "ok", label: "reading", detail: "asking the orchestrator" };

  const { control, window, now } = c;
  const stillEncoding = encoding ? "one file is still encoding" : "";

  if (control.paused) {
    const since = control.paused_at ? `since ${clock(control.paused_at)} · ${relative(control.paused_at, now)}` : "paused";
    return {
      tone: "paused",
      label: "paused",
      detail: stillEncoding ? `${since} · ${stillEncoding}` : since,
    };
  }

  switch (control.override) {
    case "open":
      return {
        tone: "forced",
        label: "forced open",
        detail: control.override_until
          ? `forced open until ${clock(control.override_until)} · then the schedule`
          : "forced open until you close it",
      };
    case "close":
      return {
        tone: "forced",
        label: "forced shut",
        detail: stillEncoding ? `no new claims · ${stillEncoding}` : "no new claims start",
      };
    default:
      return window.open
        ? { tone: "ok", label: "open", detail: `schedule ${window.spec}` }
        : { tone: "ok", label: "shut", detail: `opens ${window.next_at ?? "?"}` };
  }
}

/** The one-line result of the last action, for the strip's quiet feedback. */
export function actionNote(result: {
  applied: number;
  refused?: { id: number; state?: string }[] | null;
}): string {
  const refused = result.refused ?? [];
  if (result.applied === 0 && refused.length === 0) return "nothing to do";
  const head = `${result.applied} file${result.applied === 1 ? "" : "s"}`;
  if (refused.length === 0) return head;
  const first = refused[0];
  const why = first.state ? `in ${first.state}` : "gone";
  return `${head} · ${refused.length} refused (${why})`;
}
