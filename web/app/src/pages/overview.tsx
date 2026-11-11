import { ArrowRight, ClockCounterClockwise, FilmStrip } from "@phosphor-icons/react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import { Page } from "@/components/page";
import { Section } from "@/components/section";
import { usePulse } from "@/components/stream-provider";
import { TickStrip } from "@/components/tick-strip";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { Stat } from "@/components/ui/stat";
import { StatusBadge } from "@/components/ui/status-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, asApiError } from "@/lib/api";
import { bytes, clock, day, gb, pct, timeOfDay } from "@/lib/format";
import { qk } from "@/lib/keys";
import { idleNote, windowLabel } from "@/lib/schedule";
import { statePresentation } from "@/lib/status";
import type { Run } from "@/lib/types";

export function OverviewPage() {
  const status = useQuery({
    queryKey: qk.status,
    queryFn: api.status,
    staleTime: 5_000,
    // The floor, not the mechanism: the stream writes these numbers as they
    // move. If the stream is down this refetch is what keeps the page honest.
    refetchInterval: 30_000,
  });
  const runs = useQuery({ queryKey: qk.runs(5), queryFn: () => api.runs(5) });
  const events = useQuery({ queryKey: qk.events(14), queryFn: () => api.events(14) });
  const { pulse, connected } = usePulse();
  const statusError = status.error ? asApiError(status.error) : null;

  const dash = pulse?.dashboard ?? status.data?.dashboard ?? null;
  const live = pulse?.live ?? status.data?.live ?? null;
  const window = pulse?.window ?? status.data?.window ?? null;

  if (statusError && !dash) {
    return (
      <Page>
        <ErrorState
          code={statusError.code}
          title="The orchestrator did not answer"
          description={statusError.message}
          actions={<Button onClick={() => void status.refetch()}>Try again</Button>}
        />
      </Page>
    );
  }

  if (!dash) {
    return (
      <Page>
        <div className="flex flex-col gap-3">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      </Page>
    );
  }

  const encodable = (dash.decisions.encode ?? 0) + (dash.decisions.remux ?? 0);
  const done = dash.states.done ?? 0;

  return (
    <Page>
      <PageHeader
        eyebrow="the reclaim"
        title={`${gb(dash.reclaimed_bytes)} GB back to disk`}
        description={
          <>
            {done} {done === 1 ? "file" : "files"} rewritten · {bytes(dash.total_bytes)} in the library
            {connected ? "" : " · live stream lost, figures are from the last fetch"}
          </>
        }
        actions={
          <>
            <Button asChild variant="outline">
              <Link to="/runs">
                All runs
                <ArrowRight weight="bold" />
              </Link>
            </Button>
            <Button asChild variant="outline">
              <Link to="/queue">
                The queue
                <ArrowRight weight="bold" />
              </Link>
            </Button>
          </>
        }
      />

      <Section eyebrow="the count" aside={`projected ${gb(dash.projected_bytes)} GB still queued`}>
        <div className="grid grid-cols-2 gap-6 border border-border bg-[var(--surface)] p-4 md:grid-cols-4">
          <Stat label="To encode" value={dash.decisions.encode ?? 0} hint="files the planner will rewrite" />
          <Stat label="To remux" value={dash.decisions.remux ?? 0} hint="tracks worth moving" />
          <Stat label="Left alone" value={dash.decisions.skip ?? 0} hint="already lean, or too small" />
          <Stat
            label="Failed"
            value={dash.failures}
            hint={dash.failures ? "parked after the retry budget" : "none parked"}
            trend={dash.failures ? { value: "needs a look", tone: "danger" } : undefined}
          />
        </div>
      </Section>

      <Section eyebrow="the gate" aside={`${done} / ${encodable} encodable`}>
        <TickStrip done={done} encodable={encodable} running={Boolean(live)} />
        <p className="mt-2 text-xs text-[var(--text-3)]">
          One tick per 1.67% of the queue · {live ? "amber is running now" : "filled as files complete"}
        </p>
      </Section>

      <Section eyebrow="now" aside={live ? "in flight" : windowLabel(window)}>
        {live ? (
          <Card className="border-border" data-live-file={live.file_id}>
            <CardContent className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <Link
                  to={`/files/${live.file_id}`}
                  state={{ from: { to: "/", label: "Overview" } }}
                  className="min-w-0 truncate font-mono text-[12px] text-[var(--foreground)] underline-offset-4 hover:underline"
                  title={live.path}
                >
                  {live.path}
                </Link>
                <StatusBadge status={statePresentation(live.stage).status}>
                  {statePresentation(live.stage).label}
                </StatusBadge>
              </div>
              <Progress value={Math.round(live.fraction * 100)} aria-label="Encode progress" />
              <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1 text-xs text-[var(--text-2)]">
                <span className="m-data">
                  {/* Live.Since is the last heartbeat, not the job's start: an
                      "elapsed" figure here would be a made-up number. */}
                  {pct(live.fraction * 100)} · {live.speed.toFixed(1)}× · {live.target_kbps} kbps target
                </span>
                <span className="m-data">
                  {bytes(live.bytes)} written
                </span>
              </div>
            </CardContent>
          </Card>
        ) : (
          <EmptyState
            icon={<FilmStrip weight="fill" />}
            title="Nothing encoding"
            description={idleNote(window)}
          />
        )}
      </Section>

      <Section
        eyebrow="the runs"
        aside={
          <>
            {runs.data?.runs.length ?? 5} recent
          </>
        }
      >
        {runs.data && runs.data.runs.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="m-label">Window</TableHead>
                <TableHead className="m-label text-right">Done</TableHead>
                <TableHead className="m-label text-right">Back</TableHead>
                <TableHead className="m-label hidden text-right md:table-cell">VMAF</TableHead>
                <TableHead className="m-label hidden text-right md:table-cell">Busy</TableHead>
                <TableHead className="m-label text-right">Failed</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {runs.data.runs.map((r) => (
                <RunRow key={r.Start} run={r} />
              ))}
            </TableBody>
          </Table>
        ) : (
          <EmptyState
            title="No run has closed yet"
            description={runs.data ? runs.data.schedule.weekday : "Reading the schedule…"}
          />
        )}
      </Section>

      <Section eyebrow="activity" aside={`last ${events.data?.events.length ?? 0}`}>
        {events.data && events.data.events.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="m-label w-20">Time</TableHead>
                <TableHead className="m-label hidden w-44 sm:table-cell">Code</TableHead>
                <TableHead className="m-label">Detail</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {events.data.events.map((e, i) => (
                <TableRow key={`${e.at}-${i}`}>
                  <TableCell className="m-data whitespace-nowrap text-[var(--text-2)]">
                    {clock(e.at)}
                  </TableCell>
                  <TableCell className="m-data hidden whitespace-nowrap sm:table-cell">{e.code || "—"}</TableCell>
                  <TableCell className="text-[var(--text-2)]">
                    {e.file_id ? (
                      <Link
                        to={`/files/${e.file_id}`}
                        state={{ from: { to: "/", label: "Overview" } }}
                        className="underline-offset-4 hover:underline"
                      >
                        {e.message}
                      </Link>
                    ) : (
                      e.message
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <EmptyState
            icon={<ClockCounterClockwise weight="fill" />}
            title="No activity recorded yet"
            description="The node posts an event when it claims, finishes or fails a file."
          />
        )}
      </Section>
    </Page>
  );
}

function RunRow({ run }: { run: Run }) {
  const back = Math.max(0, run.bytes_before - run.bytes_after);
  return (
    <TableRow>
      <TableCell className="whitespace-nowrap">
        <Link to={`/runs/${run.id}`} className="underline-offset-4 hover:underline">
          <span className="text-[var(--text-2)]">{day(run.Start)}</span>{" "}
          <span className="m-data text-[var(--text-3)]">
            {timeOfDay(run.Start)}–{timeOfDay(run.End)}
          </span>
        </Link>
        {run.Open ? (
          <span className="ml-2 align-middle">
            <StatusBadge status="neutral">live</StatusBadge>
          </span>
        ) : null}
      </TableCell>
      <TableCell className="m-data text-right">
        {run.done}
        {run.encoded > 0 ? (
          <span className="text-[var(--text-3)]"> · {run.encoded} encoded</span>
        ) : null}
      </TableCell>
      <TableCell className="m-data text-right">{gb(back)} GB</TableCell>
      <TableCell className="m-data hidden text-right md:table-cell">
        {run.vmaf_n ? run.vmaf_mean?.toFixed(1) : "—"}
      </TableCell>
      <TableCell className="m-data hidden text-right md:table-cell">{pct(run.busy_percent)}</TableCell>
      <TableCell className="m-data text-right">
        {run.failed ? (
          <span className="text-destructive">{run.failed}</span>
        ) : (
          <span className="text-[var(--text-3)]">0</span>
        )}
      </TableCell>
    </TableRow>
  );
}
