import { Pause, Play } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { usePulse } from "@/components/stream-provider";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StatusBadge } from "@/components/ui/status-badge";
import { api, asApiError } from "@/lib/api";
import { controlSummary } from "@/lib/control";
import { qk } from "@/lib/keys";
import type { ControlResponse } from "@/lib/types";

const DURATIONS = [
  { value: "60", label: "for 1 h" },
  { value: "120", label: "for 2 h" },
  { value: "240", label: "for 4 h" },
  { value: "0", label: "until closed" },
];

/**
 * The steering strip: the one place that answers "who is in charge of the
 * queue right now, and can I change it".
 *
 * It sits on the overview and the queue because those are the two screens an
 * operator watches. The state it shows is the server's decision (`claiming`),
 * not a guess made from the config: if the strip says open, a node's claim
 * will be accepted.
 */
export function ControlStrip() {
  const qc = useQueryClient();
  const control = useQuery({
    queryKey: qk.control,
    queryFn: api.control,
    staleTime: 5_000,
    refetchInterval: 30_000,
  });
  const { pulse } = usePulse();
  const [minutes, setMinutes] = useState("120");
  const [note, setNote] = useState("");

  const settle = (res: ControlResponse, text: string) => {
    qc.setQueryData(qk.control, res);
    void qc.invalidateQueries({ queryKey: qk.status });
    setNote(text);
  };

  const pause = useMutation({
    mutationFn: (paused: boolean) => api.pause(paused),
    onSuccess: (res) => settle(res, res.control.paused ? "queue paused" : "queue resumed"),
  });
  const win = useMutation({
    mutationFn: (v: { action: "open" | "close" | "auto"; minutes?: number }) =>
      api.window(v.action, v.minutes ?? 0),
    onSuccess: (res, v) =>
      settle(
        res,
        v.action === "open" ? "window forced open" : v.action === "close" ? "no new claims" : "schedule in charge",
      ),
  });

  const data = control.data ?? null;
  const summary = controlSummary(data, Boolean(pulse?.live));
  const paused = Boolean(data?.control.paused);
  const override = data?.control.override ?? "";
  const busy = pause.isPending || win.isPending;
  const error = control.error ? asApiError(control.error) : null;

  const badge =
    summary.tone === "paused" ? "warning" : summary.tone === "forced" ? "neutral" : summary.label === "open" ? "success" : "neutral";

  return (
    <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border border-border bg-[var(--surface)] px-3 py-2">
      <div className="flex min-w-0 items-center gap-2.5">
        <StatusBadge status={badge}>{summary.label}</StatusBadge>
        <span className="m-data truncate text-[var(--text-2)]" aria-live="polite">
          {summary.detail}
          {note ? <span className="text-[var(--text-3)]"> · {note}</span> : null}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant={paused ? "default" : "outline"}
          disabled={busy}
          onClick={() => pause.mutate(!paused)}
        >
          {paused ? <Play weight="bold" /> : <Pause weight="bold" />}
          {paused ? "Resume" : "Pause"}
        </Button>

        <Select value={minutes} onValueChange={setMinutes}>
          <SelectTrigger aria-label="How long to force the window open" className="h-7 text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {DURATIONS.map((d) => (
              <SelectItem key={d.value} value={d.value}>
                {d.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Button
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={() => win.mutate({ action: "open", minutes: Number(minutes) })}
        >
          Open now
        </Button>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => win.mutate({ action: "close" })}>
          Stop starting work
        </Button>
        {override ? (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => win.mutate({ action: "auto" })}>
            Follow schedule
          </Button>
        ) : null}
      </div>

      {error ? (
        <p className="m-0 w-full text-xs text-[var(--destructive)]" role="alert">
          {error.code}: {error.message}
        </p>
      ) : null}
    </div>
  );
}
