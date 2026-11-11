import { ClockCounterClockwise, GitDiff } from "@phosphor-icons/react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { BackButton } from "@/components/back-button";
import { DiffDialog } from "@/components/diff-dialog";
import { Page } from "@/components/page";
import { RefreshButton } from "@/components/refresh-button";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, asApiError } from "@/lib/api";
import { stamp } from "@/lib/format";
import { qk } from "@/lib/keys";
import type { Diff, Revision } from "@/lib/types";

/**
 * Every configuration this system has run, newest first.
 *
 * A revision is append-only: restoring an old one adds a new row rather than
 * removing the ones in between, so this list is a record of decisions and not a
 * cursor over a mutable value.
 */
export function ConfigHistoryPage() {
  const list = useQuery({ queryKey: qk.configHistory(50), queryFn: () => api.configHistory(50) });
  const [comparing, setComparing] = useState<{ rev: Revision; previous: number } | null>(null);
  const [diff, setDiff] = useState<Diff | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const error = list.error ? asApiError(list.error) : null;
  const back = <BackButton parent="/config" />;

  if (error) {
    return (
      <Page>
        <ErrorState code={error.code} title="The history did not load" description={error.message} actions={back} />
      </Page>
    );
  }

  const open = async (rev: Revision, previous: number) => {
    setBusy(true);
    setErr(null);
    try {
      setDiff(await api.configDiff(previous, rev.rev));
      setComparing({ rev, previous });
    } catch (e) {
      setErr(asApiError(e).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Page>
      <PageHeader
        eyebrow="config history"
        title="Every version the pipeline has run"
        description={
          list.data ? (
            <>
              {list.data.revisions.length} revisions · head{" "}
              <span className="m-data">{list.data.state.head_rev || "—"}</span>
              {list.data.state.dirty ? " · the file on disk does not match it" : ""}
            </>
          ) : (
            "…"
          )
        }
        actions={
          <>
            {back}
            <RefreshButton busy={list.isFetching} onRefresh={() => void list.refetch()} />
          </>
        }
      />

      {err ? <p className="text-sm text-destructive">{err}</p> : null}

      <Section eyebrow="revisions" aside="newest first">
        {!list.data ? (
          <Skeleton className="h-64 w-full" />
        ) : list.data.revisions.length === 0 ? (
          <EmptyState title="No revisions yet" description="The first save creates one." />
        ) : (
          <Card className="overflow-hidden">
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="w-16 text-right">Rev</TableHead>
                    <TableHead>What changed</TableHead>
                    <TableHead>By</TableHead>
                    <TableHead>When</TableHead>
                    <TableHead className="w-24 text-right">
                      <span className="sr-only">Compare with the previous revision</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.data.revisions.map((r, i) => {
                    const previous = list.data!.revisions[i + 1]?.rev ?? 0;
                    return (
                      <TableRow key={r.rev}>
                        <TableCell className="m-data text-right">{r.rev}</TableCell>
                        <TableCell>
                          <span className="flex flex-col gap-0.5">
                            <span>{r.message}</span>
                            <span className="m-data text-[var(--text-3)]">{r.sha.slice(0, 12)}</span>
                          </span>
                        </TableCell>
                        <TableCell>
                          <StatusBadge status={r.actor === "boot" ? "neutral" : "success"}>{r.actor}</StatusBadge>
                        </TableCell>
                        <TableCell className="m-data whitespace-nowrap text-[var(--text-2)]">{stamp(r.created_at)}</TableCell>
                        <TableCell className="text-right">
                          <Button variant="ghost" size="xs" onClick={() => void open(r, previous)} disabled={busy}>
                            <GitDiff className="size-3.5" /> Changes
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        )}
      </Section>

      <Section eyebrow="what this is">
        <p className="max-w-[70ch] text-sm text-muted-foreground">
          <ClockCounterClockwise className="mr-1 inline size-4" />
          Revisions live in the state database, not in git: the image is distroless and the config directory is not a
          repository. Each row holds the whole document, so a restore is a new revision with old values and nothing is
          ever deleted. A hand edit on the volume is recorded when it is adopted, which is what makes the dirty notice
          on the config screen meaningful.
        </p>
      </Section>

      <DiffDialog
        open={comparing !== null}
        onOpenChange={(v) => (v ? null : setComparing(null))}
        diff={diff}
        problems={[]}
        busy={busy}
        title={comparing ? `Revision ${comparing.rev.rev} — ${comparing.rev.message}` : ""}
        confirmLabel="Close"
        onConfirm={() => setComparing(null)}
      />
    </Page>
  );
}
