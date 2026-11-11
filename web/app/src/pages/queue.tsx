import { ArrowLeft, ArrowRight, MagnifyingGlass } from "@phosphor-icons/react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { ControlStrip } from "@/components/control-strip";
import { Page } from "@/components/page";
import { RefreshButton } from "@/components/refresh-button";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { PageToolbar } from "@/components/ui/page-toolbar";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, asApiError, type FileQuery, type QueueAction } from "@/lib/api";
import { actionNote } from "@/lib/control";
import { bytes, fileName, gb, kbps, pct, stamp } from "@/lib/format";
import { qk } from "@/lib/keys";
import { columnsFor, queueHref, TABS, tabFrom, type Column } from "@/lib/queue";
import { statePresentation } from "@/lib/status";
import type { FileRow } from "@/lib/types";

const PER_PAGE = 100;

/** How each column presents itself, and what it is called. */
const META: Record<Column, { label: string; right?: boolean; hide?: string }> = {
  file: { label: "File" },
  state: { label: "State" },
  why: { label: "Why", hide: "hidden md:table-cell" },
  source: { label: "Source", right: true, hide: "hidden md:table-cell" },
  target: { label: "Target", right: true, hide: "hidden md:table-cell" },
  size: { label: "Size", right: true },
  finished: { label: "Finished", hide: "hidden sm:table-cell" },
  saved: { label: "Saved", right: true },
  vmaf: { label: "VMAF", right: true, hide: "hidden md:table-cell" },
};

export function QueuePage() {
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const tab = tabFrom(params);
  const page = Math.max(1, Number(params.get("page") ?? "1") || 1);
  const q = params.get("q") ?? "";

  const [search, setSearch] = useState(q);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [note, setNote] = useState("");

  const filter: FileQuery = {
    ...tab.query,
    q: q || undefined,
    limit: PER_PAGE + 1,
    offset: (page - 1) * PER_PAGE,
  };
  const files = useQuery({
    queryKey: qk.files(filter),
    queryFn: () => api.files(filter),
    placeholderData: keepPreviousData,
    staleTime: 10_000,
  });
  const counts = useQuery({ queryKey: qk.queue, queryFn: api.queue, staleTime: 10_000 });
  const filesError = files.error ? asApiError(files.error) : null;

  // A row selected on another tab is not on screen, and acting on it would be a
  // surprise: selection belongs to what the operator is looking at.
  useEffect(() => {
    setSelected(new Set());
    setNote("");
  }, [tab.key, page, q]);

  const act = useMutation({
    mutationFn: (v: { action: QueueAction; ids: number[]; priority?: number }) =>
      api.act(v.action, v.ids, v.priority ?? 0),
    onSuccess: (res) => {
      setNote(actionNote(res));
      setSelected(new Set());
      void qc.invalidateQueries({ queryKey: ["files"] });
      void qc.invalidateQueries({ queryKey: qk.queue });
    },
  });

  const rows = files.data?.files.slice(0, PER_PAGE) ?? [];
  const hasMore = (files.data?.files.length ?? 0) > PER_PAGE;
  const total = files.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PER_PAGE));
  const columns = columnsFor(tab.key);

  function goToTab(key: string) {
    const next = new URLSearchParams();
    if (key !== TABS[0].key) next.set("tab", key);
    if (q) next.set("q", q);
    setParams(next, { replace: true });
  }

  function goToPage(n: number) {
    const next = new URLSearchParams(params);
    if (n <= 1) next.delete("page");
    else next.set("page", String(n));
    setParams(next);
  }

  function submitSearch(e: React.FormEvent) {
    e.preventDefault();
    const next = new URLSearchParams(params);
    const term = search.trim();
    if (term) next.set("q", term);
    else next.delete("q");
    next.delete("page");
    setParams(next);
  }

  const pageIDs = rows.map((r) => r.id);
  const allSelected = pageIDs.length > 0 && pageIDs.every((id) => selected.has(id));
  const someSelected = pageIDs.some((id) => selected.has(id));

  function toggleAll() {
    const next = new Set(selected);
    if (allSelected) pageIDs.forEach((id) => next.delete(id));
    else pageIDs.forEach((id) => next.add(id));
    setSelected(next);
  }

  function toggleOne(id: number) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  }

  const ids = [...selected];
  const actLabel =
    tab.key === "done" || tab.key === "inflight"
      ? "Read-only on this tab: the encoder owns a running file and a replaced one is a fact."
      : null;

  return (
    <Page>
      <PageHeader
        eyebrow="the queue"
        title="Every file the planner has an opinion about"
        description="Filtered by state, one page at a time. A file leaves the queue only when it is done or retired."
        actions={<RefreshButton onRefresh={() => void files.refetch()} busy={files.isFetching} />}
      />

      <ControlStrip />

      <Section eyebrow="filter" aside={files.data ? `${total} rows` : undefined}>
        <PageToolbar
          left={
            <div className="flex max-w-full flex-col gap-2">
              <div className="max-w-full overflow-x-auto">
                <Tabs value={tab.key} onValueChange={goToTab}>
                  <TabsList>
                    {TABS.map((t) => {
                      const n = counts.data?.counts[t.key];
                      return (
                        <TabsTrigger key={t.key} value={t.key}>
                          {t.label}
                          {n !== undefined ? (
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
              <form onSubmit={submitSearch} className="flex items-center gap-2">
                <label className="relative w-full max-w-xs">
                  <span className="sr-only">Find by path</span>
                  <MagnifyingGlass
                    size={14}
                    aria-hidden="true"
                    className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-[var(--text-3)]"
                  />
                  <Input
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    placeholder="find by path…"
                    className="pl-7"
                  />
                </label>
                <Button type="submit" size="sm" variant="outline">
                  Find
                </Button>
                {q ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setSearch("");
                      const next = new URLSearchParams(params);
                      next.delete("q");
                      next.delete("page");
                      setParams(next);
                    }}
                  >
                    Clear
                  </Button>
                ) : null}
              </form>
            </div>
          }
          right={
            <span className="m-label hidden sm:inline">
              page {page} of {pages}
            </span>
          }
        />

        {note ? (
          <p className="m-0 mt-3 text-xs text-[var(--text-2)]" role="status">
            {note}
          </p>
        ) : null}
        {act.error ? (
          <p className="m-0 mt-3 text-xs text-[var(--destructive)]" role="alert">
            {asApiError(act.error).code}: {asApiError(act.error).message}
          </p>
        ) : null}

        {selected.size > 0 ? (
          <div className="mt-3 flex flex-wrap items-center gap-2 border border-border bg-[var(--surface)] px-3 py-2">
            <span className="m-label">{selected.size} selected</span>
            <Button size="sm" variant="outline" disabled={act.isPending} onClick={() => act.mutate({ action: "retry", ids })}>
              Retry
            </Button>
            <Button size="sm" variant="outline" disabled={act.isPending} onClick={() => act.mutate({ action: "skip", ids })}>
              Skip
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={act.isPending}
              onClick={() => act.mutate({ action: "priority", ids, priority: 100 })}
            >
              Next in queue
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={act.isPending}
              onClick={() => act.mutate({ action: "priority", ids, priority: 0 })}
            >
              Normal
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={act.isPending}
              onClick={() => act.mutate({ action: "priority", ids, priority: -100 })}
            >
              Send to back
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
              Clear
            </Button>
          </div>
        ) : null}

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
              title={q ? "Nothing matches that path" : "Nothing in this view"}
              description={
                q
                  ? "The find box searches the full path, not the file name."
                  : actLabel ?? "The planner has either not run, or found nothing matching this filter."
              }
              action={
                <Button variant="outline" asChild>
                  <Link to="/queue">Show the queue</Link>
                </Button>
              }
            />
          </div>
        ) : (
          <div className="mt-4">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-8">
                    <Checkbox
                      checked={allSelected}
                      indeterminate={someSelected && !allSelected}
                      onChange={toggleAll}
                      aria-label="Select every row on this page"
                    />
                  </TableHead>
                  {columns.map((c) => (
                    <TableHead
                      key={c}
                      className={[META[c].right ? "text-right" : "", META[c].hide ?? ""].join(" ").trim()}
                    >
                      {META[c].label}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row) => (
                  <TableRow key={row.id}>
                    <TableCell>
                      <Checkbox
                        checked={selected.has(row.id)}
                        onChange={() => toggleOne(row.id)}
                        aria-label={`Select ${fileName(row.path)}`}
                      />
                    </TableCell>
                    {columns.map((c) => (
                      <Cell key={c} row={row} col={c} tabKey={tab.key} page={page} q={q} />
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>

            <div className="mt-4 flex items-center justify-between">
              <Button variant="outline" disabled={page <= 1} onClick={() => goToPage(page - 1)} aria-disabled={page <= 1}>
                <ArrowLeft weight="bold" />
                Previous
              </Button>
              <span className="m-label">
                page {page} of {pages} · {total} rows
              </span>
              <Button variant="outline" disabled={!hasMore && page >= pages} onClick={() => goToPage(page + 1)}>
                Next
                <ArrowRight weight="bold" />
              </Button>
            </div>
          </div>
        )}
      </Section>

      {actLabel ? (
        <p className="m-0 text-xs text-[var(--text-3)]">{actLabel}</p>
      ) : null}
    </Page>
  );
}

function Cell({
  row,
  col,
  tabKey,
  page,
  q,
}: {
  row: FileRow;
  col: Column;
  tabKey: string;
  page: number;
  q: string;
}) {
  const meta = META[col];
  const cls = [meta.right ? "text-right" : "", meta.hide ?? ""].join(" ").trim();

  switch (col) {
    case "file":
      return (
        <TableCell className="max-w-[10rem] truncate sm:max-w-[22rem]">
          <Link
            to={`/files/${row.id}`}
            state={{ from: { to: queueHref(tabKey, page, q), label: "Queue" } }}
            title={row.path}
            className="underline-offset-4 hover:underline"
          >
            {fileName(row.path)}
          </Link>
        </TableCell>
      );
    case "state": {
      const p = statePresentation(row.state);
      return (
        <TableCell className="whitespace-nowrap">
          <StatusBadge status={p.status}>{p.label}</StatusBadge>
          {row.priority > 0 ? <span className="m-label ml-2 text-[var(--text-2)]">next</span> : null}
          {row.priority < 0 ? <span className="m-label ml-2">last</span> : null}
        </TableCell>
      );
    }
    case "why":
      return <TableCell className={cls}>{row.reason_code || "—"}</TableCell>;
    case "source":
      return <TableCell className={cls}>{row.bitrate_kbps ? kbps(row.bitrate_kbps) : "—"}</TableCell>;
    case "target":
      return <TableCell className={cls}>{row.target_kbps ? kbps(row.target_kbps) : "—"}</TableCell>;
    case "size":
      return <TableCell className={`m-data ${cls}`}>{bytes(row.size_bytes)}</TableCell>;
    case "finished":
      return <TableCell className={`m-data ${cls}`}>{stamp(row.finished_at ?? 0)}</TableCell>;
    case "saved": {
      // A remux can grow a file (an added track), and a done row from before
      // the finish payload carried sizes has none: show the honest delta and
      // never invent a percentage over a zero denominator.
      const delta = (row.bytes_after ?? 0) - (row.bytes_before ?? 0);
      if (!row.bytes_before || delta === 0) {
        return (
          <TableCell className={`m-data ${cls}`}>
            {row.decision === "remux" ? <span className="text-[var(--text-3)]">remux</span> : "—"}
          </TableCell>
        );
      }
      if (delta > 0) {
        return (
          <TableCell className={`m-data ${cls}`}>
            {gb(delta)} GB <span className="text-[var(--text-3)]">{pct((delta / row.bytes_before) * 100)}</span>
          </TableCell>
        );
      }
      return (
        <TableCell className={`m-data ${cls} text-[var(--text-3)]`}>+{gb(-delta)} GB</TableCell>
      );
    }
    case "vmaf":
      return <TableCell className={`m-data ${cls}`}>{row.vmaf ? row.vmaf.toFixed(1) : "—"}</TableCell>;
  }
}
