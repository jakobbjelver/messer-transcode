import { useQuery } from "@tanstack/react-query";

import { Link, useParams } from "react-router-dom";

import { BackButton, PageBack } from "@/components/back-button";
import { Page } from "@/components/page";
import { Section } from "@/components/section";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Stat } from "@/components/ui/stat";
import { StatusBadge } from "@/components/ui/status-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, asApiError } from "@/lib/api";
import { day, gb, pct, saving, stamp, timeOfDay } from "@/lib/format";
import { qk } from "@/lib/keys";
import { statePresentation } from "@/lib/status";
import type { FileRow } from "@/lib/types";

/**
 * One run: what a single occurrence of the window actually did.
 *
 * The run has an id now (the ledger's, which is the window's start second), so
 * this page can exist at all — a run used to be arithmetic over the current
 * schedule, which meant it had no address and no history of its own.
 */
export function RunPage() {
  const { id } = useParams();
  const runID = Number(id);
  const valid = Number.isFinite(runID) && runID > 0;
  const detail = useQuery({ queryKey: qk.run(runID), queryFn: () => api.run(runID), enabled: valid });
  const back = <BackButton parent="/runs" />;
  const backLink = <PageBack parent="/runs" />;
  const error = detail.error ? asApiError(detail.error) : null;

  if (!valid) {
    return (
      <Page>
        <ErrorState code="E_BAD_ID" title="That is not a run" description="The address carries no run id." actions={back} />
      </Page>
    );
  }

  if (error) {
    return (
      <Page>
        <ErrorState
          code={error.code}
          title={error.status === 404 ? "No such run" : "The run did not load"}
          description={error.message}
          actions={back}
        />
      </Page>
    );
  }

  if (!detail.data) {
    return (
      <Page>
        <div className="flex flex-col gap-3">
          <Skeleton className="h-8 w-80" />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      </Page>
    );
  }

  const { run, placed, failed, skipped, open } = detail.data;
  const reclaimed = Math.max(0, run.bytes_before - run.bytes_after);
  const when = `${day(run.Start)} · ${timeOfDay(run.Start)}–${run.End ? timeOfDay(run.End) : "open"}`;

  return (
    <Page>
      {backLink}
      <PageHeader
        eyebrow="the run"
        title={when}
        description={
          <>
            {run.Kind || "run"} · <span className="m-data">{run.Spec}</span> · {run.done} placed,{" "}
            {run.failed} failed, {run.skipped} left alone
          </>
        }
        actions={run.Open ? <StatusBadge status="neutral">live</StatusBadge> : null}
      />

      <Section eyebrow="what it did">
        <div className="grid grid-cols-2 gap-6 border border-border bg-[var(--surface)] p-4 md:grid-cols-4">
          <Stat label="Placed" value={run.done} hint={`${run.encoded} encoded · ${run.remuxed} remuxed`} />
          <Stat label="Back to disk" value={`${gb(reclaimed)} GB`} hint="measured before and after" />
          <Stat
            label="Quality"
            value={run.vmaf_n ? run.vmaf_mean?.toFixed(1) : "—"}
            hint={run.vmaf_n ? `min ${run.vmaf_min?.toFixed(1)} · ${run.vmaf_n} measured` : "no VMAF this run"}
          />
          <Stat
            label="Busy"
            value={pct(run.busy_percent)}
            hint={run.failed ? `${run.failed} failed` : "nothing failed"}
          />
        </div>
      </Section>

      <Group
        eyebrow="placed"
        rows={placed}
        empty="Nothing was placed in this run."
        runID={run.id}
        columns={["file", "state", "saved", "vmaf", "finished"]}
      />

      <Group
        eyebrow="failed"
        rows={failed}
        empty="Nothing failed."
        runID={run.id}
        columns={["file", "state", "why", "detail"]}
      />

      <Group
        eyebrow="left alone"
        rows={skipped}
        empty="Nothing was retired."
        runID={run.id}
        columns={["file", "state", "why"]}
      />

      {open.length > 0 ? (
        <Group
          eyebrow="still in flight"
          rows={open}
          empty=""
          runID={run.id}
          columns={["file", "state", "detail"]}
        />
      ) : null}
    </Page>
  );
}

type Col = "file" | "state" | "why" | "detail" | "saved" | "vmaf" | "finished";

const HEAD: Record<Col, string> = {
  file: "File",
  state: "State",
  why: "Code",
  detail: "Detail",
  saved: "Saved",
  vmaf: "VMAF",
  finished: "Finished",
};

function Group({
  eyebrow,
  rows,
  empty,
  runID,
  columns,
}: {
  eyebrow: string;
  rows: FileRow[];
  empty: string;
  runID: number;
  columns: Col[];
}) {
  return (
    <Section eyebrow={`${eyebrow} · ${rows.length}`}>
      {rows.length === 0 ? (
        <EmptyState title={empty} description="A quiet run is a fact, not a missing row." />
      ) : (
        <Card className="min-w-0 overflow-hidden">
          <CardContent className="min-w-0 p-0">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  {columns.map((c) => (
                    <TableHead
                      key={c}
                      className={c === "saved" || c === "vmaf" || c === "finished" ? "text-right" : undefined}
                    >
                      {HEAD[c]}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((f) => (
                  <TableRow key={f.id}>
                    {columns.map((c) => (
                      <Row key={c} col={c} row={f} runID={runID} />
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </Section>
  );
}

function Row({ col, row, runID }: { col: Col; row: FileRow; runID: number }) {
  switch (col) {
    case "file":
      return (
        <TableCell className="max-w-[16rem] truncate">
          <Link
            to={`/files/${row.id}`}
            state={{ from: { to: `/runs/${runID}`, label: "this run" } }}
            title={row.path}
            className="underline-offset-4 hover:underline"
          >
            {row.path.split("/").pop()}
          </Link>
        </TableCell>
      );
    case "state": {
      const p = statePresentation(row.state);
      return (
        <TableCell>
          <StatusBadge status={p.status}>{p.label}</StatusBadge>
        </TableCell>
      );
    }
    case "why":
      return <TableCell className="m-data">{row.last_error_code || row.reason_code || "—"}</TableCell>;
    case "detail":
      return (
        <TableCell className="m-data text-[var(--text-2)]">
          {row.reason || (row.attempts ? `${row.attempts} attempt(s)` : "—")}
        </TableCell>
      );
    case "saved":
      return (
        <TableCell className="m-data text-right">
          {saving((row.bytes_before ?? 0) - (row.bytes_after ?? 0))}
        </TableCell>
      );
    case "vmaf":
      return <TableCell className="m-data text-right">{row.vmaf ? row.vmaf.toFixed(1) : "—"}</TableCell>;
    case "finished":
      return <TableCell className="m-data text-right">{stamp(row.finished_at ?? 0)}</TableCell>;
  }
}
