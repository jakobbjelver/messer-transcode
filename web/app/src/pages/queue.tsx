import { ArrowLeft, ArrowRight } from "@phosphor-icons/react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";

import { Page } from "@/components/page";
import { Section } from "@/components/section";
import { usePulse } from "@/components/stream-provider";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { PageToolbar } from "@/components/ui/page-toolbar";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, asApiError } from "@/lib/api";
import { bytes, fileName } from "@/lib/format";
import { qk } from "@/lib/keys";
import { statePresentation } from "@/lib/status";

const PER_PAGE = 100;

type Filter = { key: string; label: string; state?: string; decision?: string };

const FILTERS: Filter[] = [
  { key: "all", label: "All" },
  { key: "encode", label: "To encode", decision: "encode" },
  { key: "remux", label: "To remux", decision: "remux" },
  { key: "skip", label: "Left alone", decision: "skip" },
  { key: "failed", label: "Failed", state: "failed" },
];

export function QueuePage() {
  const [params, setParams] = useSearchParams();
  const state = params.get("state") ?? "";
  const decision = params.get("decision") ?? "";
  const page = Math.max(1, Number(params.get("page") ?? "1") || 1);

  const active =
    FILTERS.find((f) => (f.state ?? "") === state && (f.decision ?? "") === decision)?.key ?? "all";

  // Where a detail page should come back to: this filter, this page.
  const here = "/queue" + (params.toString() ? `?${params.toString()}` : "");

  const filter = {
    state: state || undefined,
    decision: decision || undefined,
    limit: PER_PAGE + 1,
    offset: (page - 1) * PER_PAGE,
  };
  const files = useQuery({
    queryKey: qk.files(filter),
    queryFn: () => api.files(filter),
    // A page change keeps the previous rows on screen instead of flashing
    // skeletons: the table is 100 rows and the fetch is local.
    placeholderData: keepPreviousData,
    staleTime: 10_000,
  });
  const filesError = files.error ? asApiError(files.error) : null;
  const status = useQuery({ queryKey: qk.status, queryFn: api.status, staleTime: 5_000 });
  const { pulse } = usePulse();
  const dash = pulse?.dashboard ?? status.data?.dashboard ?? null;

  // One more row than a page is the answer to "is there a next page".
  const rows = files.data?.files.slice(0, PER_PAGE) ?? [];
  const hasMore = (files.data?.files.length ?? 0) > PER_PAGE;

  function select(key: string) {
    const f = FILTERS.find((x) => x.key === key) ?? FILTERS[0];
    const next = new URLSearchParams();
    if (f.state) next.set("state", f.state);
    if (f.decision) next.set("decision", f.decision);
    setParams(next, { replace: true });
  }

  function goToPage(n: number) {
    const next = new URLSearchParams(params);
    if (n <= 1) next.delete("page");
    else next.set("page", String(n));
    setParams(next);
  }

  const count = (f: Filter) => {
    if (!dash) return null;
    if (f.state === "failed") return dash.failures;
    if (f.state) return dash.states[f.state] ?? 0;
    if (f.decision) return dash.decisions[f.decision] ?? 0;
    return dash.total_files;
  };

  return (
    <Page>
      <PageHeader
        eyebrow="the queue"
        title="Every file the planner has an opinion about"
        description="Filtered by decision, one page at a time. A file leaves this list only when it is done or parked."
      />

      <Section eyebrow="filter">
        <PageToolbar
          left={
            // Four filters plus counts outgrow a 390px row; the strip scrolls
            // inside its own box rather than pushing the page sideways.
            <div className="max-w-full overflow-x-auto">
              <Tabs value={active} onValueChange={select}>
                <TabsList>
                  {FILTERS.map((f) => {
                    const n = count(f);
                    return (
                      <TabsTrigger key={f.key} value={f.key}>
                        {f.label}
                        {n !== null ? (
                          <span className="ml-1.5 hidden font-mono text-[10.5px] tabular-nums text-[var(--text-3)] sm:inline">
                            {n}
                          </span>
                        ) : null}
                      </TabsTrigger>
                    );
                  })}
                </TabsList>
              </Tabs>
            </div>
          }
          right={
            <span className="m-label hidden sm:inline">
              page {page} · {files.data?.count ?? 0} shown
            </span>
          }
        />

        {filesError ? (
          <div className="mt-4">
            <ErrorState
              code={filesError.code}
              title="The queue did not load"
              description={filesError.message}
              actions={<Button onClick={() => void files.refetch()}>Try again</Button>}
            />
          </div>
        ) : files.isPending && !files.data ? (
          <div className="mt-4 flex flex-col gap-2">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-9 w-full" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="mt-4">
            <EmptyState
              title="Nothing here"
              description="The planner has either not run, or found nothing matching this filter."
              action={
                <Button variant="outline" asChild>
                  <Link to="/queue">Show everything</Link>
                </Button>
              }
            />
          </div>
        ) : (
          <div className="mt-4">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="m-label">File</TableHead>
                  <TableHead className="m-label">State</TableHead>
                  <TableHead className="m-label hidden md:table-cell">Why</TableHead>
                  <TableHead className="m-label hidden text-right md:table-cell">Source</TableHead>
                  <TableHead className="m-label hidden text-right md:table-cell">Target</TableHead>
                  <TableHead className="m-label text-right">Size</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row) => {
                  const p = statePresentation(row.state);
                  return (
                    <TableRow key={row.id}>
                      <TableCell className="max-w-[10rem] truncate sm:max-w-[22rem]">
                        <Link
                          to={`/files/${row.id}`}
                          state={{ from: { to: here, label: "Queue" } }}
                          title={row.path}
                          className="underline-offset-4 hover:underline"
                        >
                          {fileName(row.path)}
                        </Link>
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={p.status}>{p.label}</StatusBadge>
                      </TableCell>
                      <TableCell className="m-data hidden whitespace-nowrap text-[var(--text-2)] md:table-cell">
                        {row.reason_code || "—"}
                      </TableCell>
                      <TableCell className="m-data hidden text-right md:table-cell">
                        {row.bitrate_kbps ? `${row.bitrate_kbps} kbps` : "—"}
                      </TableCell>
                      <TableCell className="m-data hidden text-right md:table-cell">
                        {row.target_kbps ? `${row.target_kbps} kbps` : "—"}
                      </TableCell>
                      <TableCell className="m-data text-right">{bytes(row.size_bytes)}</TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>

            <div className="mt-4 flex items-center justify-between">
              <Button
                variant="outline"
                disabled={page <= 1}
                onClick={() => goToPage(page - 1)}
                aria-disabled={page <= 1}
              >
                <ArrowLeft weight="bold" />
                Previous
              </Button>
              <span className="m-label">page {page}</span>
              <Button
                variant="outline"
                disabled={!hasMore}
                onClick={() => goToPage(page + 1)}
                aria-disabled={!hasMore}
              >
                Next
                <ArrowRight weight="bold" />
              </Button>
            </div>
          </div>
        )}
      </Section>
    </Page>
  );
}
