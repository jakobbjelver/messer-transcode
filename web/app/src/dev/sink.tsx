import { Check, Clock, FilmSlate, MagnifyingGlass, Warning } from "@phosphor-icons/react";

import { Page } from "@/components/page";
import { Section } from "@/components/section";
import { TickStrip } from "@/components/tick-strip";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { PageToolbar } from "@/components/ui/page-toolbar";
import { Progress } from "@/components/ui/progress";
import { Separator } from "@/components/ui/separator";
import { SettingsSection } from "@/components/ui/settings-section";
import { Skeleton } from "@/components/ui/skeleton";
import { Stat } from "@/components/ui/stat";
import { StatusBadge } from "@/components/ui/status-badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ThemeControl } from "@/components/theme-control";
import { Time } from "@/components/ui/time";

/**
 * Conformance sink: every atom and block this app ships, rendered from the real
 * components so `messer check` can compare computed styles against the canon.
 * Dev-only — main.tsx mounts it when the URL path is /ui and import.meta.env.DEV.
 */
export function Sink() {
  return (
    <Page>
      <PageHeader
        eyebrow="messer-ui · transcode"
        title="Kitchen sink"
        description={
          <>
            Port 1 · <code>brand-transcode</code> · measured by <code>messer check</code>
          </>
        }
        actions={
          <>
            <Button size="sm" variant="outline">
              <MagnifyingGlass />
              Search
            </Button>
            <ThemeControl />
          </>
        }
      />

      <Separator />

      <Section eyebrow="atoms">
        <div className="grid items-start gap-6 md:grid-cols-2 xl:grid-cols-3">
          <Card>
            <CardHeader>
              <CardTitle className="m-label">Type &amp; data</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3">
              <h2 data-fixture="h2">Section heading</h2>
              <h3 data-fixture="h3">Panel heading</h3>
              <p data-fixture="body" className="m-0">
                Body copy sits in Golos Text at 14px. The quick brown fox jumps over the lazy dog.
              </p>
              <p className="prose m-0" data-fixture="prose">
                Prose runs at 15px/1.6 with a 68ch measure.
              </p>
              <p className="m-0 text-xs text-[var(--text-3)]" data-fixture="small">
                Small text for hints and captions.
              </p>
              <span className="m-data" data-fixture="data">
                2026-11-10 · 14,208 ms · #4A9F
              </span>
              <span className="m-label" data-fixture="label">
                meta label
              </span>
              <Time value={Date.now()} mode="both" />
              <code data-fixture="code">messer-transcode report --last 7</code>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="m-label">Buttons</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3">
              <div className="flex flex-wrap items-center gap-2">
                <Button data-fixture="btn-primary">
                  <Check />
                  Publish
                </Button>
                <Button variant="outline" data-fixture="btn-secondary">
                  Cancel
                </Button>
                <Button variant="ghost" data-fixture="btn-ghost">
                  Skip
                </Button>
                <Button variant="destructive" data-fixture="btn-danger">
                  Delete
                </Button>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button size="sm" data-fixture="btn-sm">
                  Small
                </Button>
                <Button size="lg" data-fixture="btn-lg">
                  Large
                </Button>
                <Button size="icon" variant="outline" data-fixture="btn-icon" aria-label="Search">
                  <MagnifyingGlass />
                </Button>
                <Button disabled data-fixture="btn-disabled">
                  Disabled
                </Button>
              </div>
            </CardContent>
          </Card>

          <Card data-fixture="panel">
            <CardHeader className="flex flex-row items-center justify-between">
              <CardTitle data-fixture="panel-title">Now encoding</CardTitle>
              <StatusBadge status="warning" data-fixture="badge-warning">
                Encoding
              </StatusBadge>
            </CardHeader>
            <CardContent className="grid gap-3">
              <span className="m-data">Film One (2020) 1080p · 42% · 8.6×</span>
              <Progress value={42} data-fixture="progress" />
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge status="neutral" data-fixture="badge-neutral">
                  Idle
                </StatusBadge>
                <StatusBadge status="success" data-fixture="badge-success">
                  Done
                </StatusBadge>
                <StatusBadge status="danger" data-fixture="badge-danger">
                  Failed
                </StatusBadge>
              </div>
              <EmptyState
                title="Nothing encoding"
                description="The window is open — start the node and it will claim work."
                variant="bare"
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="m-label">Numbers</CardTitle>
            </CardHeader>
            <CardContent className="grid grid-cols-2 gap-6">
              <Stat label="To encode" value={739} hint="files the planner will rewrite" />
              <Stat label="Failed" value={8} trend={{ value: "needs a look", tone: "danger" }} />
              <Stat label="Back" value="454" hint="GB returned to disk" />
              <Stat label="Busy" value="38%" hint="encoder time vs window" />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="m-label">Data</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-4">
              <Table data-fixture="table">
                <TableHeader>
                  <TableRow>
                    <TableHead className="m-label" data-fixture="table-th">
                      File
                    </TableHead>
                    <TableHead className="m-label">State</TableHead>
                    <TableHead className="m-label text-right" data-fixture="table-num">
                      Size
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  <TableRow>
                    <TableCell>Film One.mkv</TableCell>
                    <TableCell>
                      <StatusBadge status="success">Done</StatusBadge>
                    </TableCell>
                    <TableCell className="m-data text-right">8.42 GB</TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell>Film Two.mkv</TableCell>
                    <TableCell>
                      <StatusBadge status="warning">Encoding</StatusBadge>
                    </TableCell>
                    <TableCell className="m-data text-right">2.10 GB</TableCell>
                  </TableRow>
                </TableBody>
              </Table>
              <div className="flex items-center gap-3">
                <span
                  className="grid size-8 place-items-center rounded-full bg-[var(--surface-2)]"
                  data-fixture="avatar"
                >
                  JB
                </span>
                <Badge variant="outline">
                  <span className="inline-block size-1.5 rounded-full bg-[var(--success)]" data-fixture="dot" />
                  Online
                </Badge>
              </div>
              <Skeleton className="h-8 w-full" data-fixture="skeleton" />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="m-label">States</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3">
              {/* Fixture hooks the conformance harness measures: hover, focus,
                  disabled and selected are attributes, not pseudo-classes. */}
              <div className="flex flex-wrap items-center gap-2">
                <a
                  href="#hover"
                  data-state-hover
                  data-fixture="state-btn-hover"
                  className="flex h-8 items-center border border-border bg-[var(--surface)] px-3 text-sm text-[var(--foreground)] no-underline transition-[background-color,color] duration-150 hover:bg-[var(--surface-2)]"
                >
                  Hover me
                </a>
                <Button data-state-focus data-fixture="state-btn-focus">
                  Focus
                </Button>
                <Button disabled data-state-disabled data-fixture="state-btn-disabled">
                  Disabled
                </Button>
              </div>
              <a
                href="#selected"
                aria-current="page"
                data-state-selected
                data-fixture="state-nav-active"
                className="relative flex h-8 items-center bg-primary/10 px-2 text-sm text-[var(--foreground)] no-underline after:absolute after:inset-y-0 after:left-0 after:w-0.5 after:bg-primary"
              >
                Selected row
              </a>
              <p className="m-0 text-xs text-[var(--text-3)]">
                hover is one surface step · focus is a 2px ring at 2px offset · selected is an accent
                tint with a 2px rule
              </p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="m-label">Controls &amp; states</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-4">
              <Tabs defaultValue="encode">
                <TabsList>
                  <TabsTrigger value="all">All</TabsTrigger>
                  <TabsTrigger value="encode" data-fixture="tab-selected">
                    To encode
                  </TabsTrigger>
                  <TabsTrigger value="failed">Failed</TabsTrigger>
                </TabsList>
              </Tabs>
              <PageToolbar
                left={<span className="m-label">722 shown · page 1</span>}
                right={
                  <Button size="sm" variant="outline">
                    Next
                  </Button>
                }
              />
              <TickStrip done={38} encodable={60} running />
              <div className="flex items-center gap-2 text-xs text-[var(--text-3)]">
                <Warning weight="fill" className="text-warning" />
                warning copy sits at 12.5px with an icon, never colour alone
              </div>
              <div className="flex items-center gap-2 text-xs text-[var(--text-3)]">
                <FilmSlate weight="fill" />
                <Clock weight="fill" />
                block icons at 16px
              </div>
            </CardContent>
          </Card>
        </div>
      </Section>

      <Section eyebrow="blocks">
        <div className="grid gap-6 md:grid-cols-2">
          <SettingsSection title="Build" description="Read-only block: no save bar without an onSave.">
            <dl className="grid grid-cols-[7.5rem_1fr] gap-2">
              <dt className="m-label">Version</dt>
              <dd className="text-sm">v1.0.0</dd>
              <dt className="m-label">Commit</dt>
              <dd className="m-data">aba7a7f</dd>
            </dl>
          </SettingsSection>
          <ErrorState
            code="E_NOT_FOUND"
            title="No such file"
            description="The address carries no file id."
            actions={<Button variant="outline">Back to the queue</Button>}
          />
        </div>
      </Section>
    </Page>
  );
}
