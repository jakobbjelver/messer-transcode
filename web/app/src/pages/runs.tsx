import { Page } from "@/components/page";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useApi } from "@/hooks/use-api";
import { api } from "@/lib/api";
import { gb, pct, day, timeOfDay } from "@/lib/format";
import { scheduleLabel } from "@/lib/schedule";

const LAST = 30;

export function RunsPage() {
  const runs = useApi(`runs:${LAST}`, () => api.runs(LAST));

  if (runs.error) {
    return (
      <Page>
        <ErrorState
          code={runs.error.code}
          title="The runs did not load"
          description={runs.error.message}
          actions={<Button onClick={runs.reload}>Try again</Button>}
        />
      </Page>
    );
  }

  return (
    <Page>
      <PageHeader
        eyebrow="the runs"
        title="What each window actually did"
        description={
          runs.data
            ? `One row per occurrence of the configured window · ${scheduleLabel(runs.data.schedule)}`
            : "Reading the schedule…"
        }
        actions={<Button variant="outline" onClick={runs.reload}>Refresh</Button>}
      />

      <Section eyebrow={`last ${LAST}`} aside={runs.data ? `${runs.data.runs.length} collected` : undefined}>
        {!runs.data ? (
          <div className="flex flex-col gap-2">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-9 w-full" />
            ))}
          </div>
        ) : runs.data.runs.length === 0 ? (
          <EmptyState
            title="No run has closed yet"
            description={`The window is ${runs.data.schedule.weekday} on weekdays and ${runs.data.schedule.weekend} at weekends.`}
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="m-label">Window</TableHead>
                <TableHead className="m-label hidden md:table-cell">Kind</TableHead>
                <TableHead className="m-label text-right">Done</TableHead>
                <TableHead className="m-label hidden text-right lg:table-cell">Encoded</TableHead>
                <TableHead className="m-label hidden text-right lg:table-cell">Remuxed</TableHead>
                <TableHead className="m-label text-right">Back</TableHead>
                <TableHead className="m-label hidden text-right md:table-cell">VMAF</TableHead>
                <TableHead className="m-label hidden text-right md:table-cell">Busy</TableHead>
                <TableHead className="m-label text-right">Failed</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {runs.data.runs.map((r) => (
                <TableRow key={r.Start}>
                  <TableCell className="whitespace-nowrap">
                    <span className="text-[var(--text-2)]">{day(r.Start)}</span>{" "}
                    <span className="m-data text-[var(--text-3)]">
                      {timeOfDay(r.Start)}–{timeOfDay(r.End)}
                    </span>
                    {r.Open ? (
                      <span className="ml-2 align-middle">
                        <StatusBadge status="neutral">live</StatusBadge>
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell className="m-data hidden text-[var(--text-2)] md:table-cell">
                    {r.Kind || "—"} <span className="text-[var(--text-3)]">{r.Spec}</span>
                  </TableCell>
                  <TableCell className="m-data text-right">{r.done}</TableCell>
                  <TableCell className="m-data hidden text-right lg:table-cell">{r.encoded}</TableCell>
                  <TableCell className="m-data hidden text-right lg:table-cell">{r.remuxed}</TableCell>
                  <TableCell className="m-data text-right">
                    {gb(Math.max(0, r.bytes_before - r.bytes_after))} GB
                  </TableCell>
                  <TableCell className="m-data hidden text-right md:table-cell">
                    {r.vmaf_n ? r.vmaf_mean?.toFixed(1) : "—"}
                  </TableCell>
                  <TableCell className="m-data hidden text-right md:table-cell">{pct(r.busy_percent)}</TableCell>
                  <TableCell className="m-data text-right">
                    {r.failed ? (
                      <span className="text-destructive">{r.failed}</span>
                    ) : (
                      <span className="text-[var(--text-3)]">0</span>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
    </Page>
  );
}
