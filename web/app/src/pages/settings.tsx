import { Sliders } from "@phosphor-icons/react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import { Page } from "@/components/page";
import { RefreshButton } from "@/components/refresh-button";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { SettingsSection } from "@/components/ui/settings-section";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Time } from "@/components/ui/time";
import { api, asApiError } from "@/lib/api";
import { bytes, duration } from "@/lib/format";
import { qk } from "@/lib/keys";
import type { HealthCheck } from "@/lib/types";

const CHECK_STATUS = {
  ok: "success",
  degraded: "warning",
  broken: "danger",
} as const;

export function SettingsPage() {
  const config = useQuery({ queryKey: qk.config, queryFn: api.config });
  const health = useQuery({ queryKey: qk.health, queryFn: api.health, staleTime: 30_000 });
  const configError = config.error ? asApiError(config.error) : null;
  const healthError = health.error ? asApiError(health.error) : null;

  if (configError) {
    return (
      <Page>
        <ErrorState
          code={configError.code}
          title="Settings did not load"
          description={configError.message}
          actions={<Button onClick={() => void config.refetch()}>Try again</Button>}
        />
      </Page>
    );
  }

  return (
    <Page>
      <PageHeader
        eyebrow="settings"
        title="What this orchestrator is and what it is made of"
        description="The build, the store, and the checks. The configuration has its own screen now, with its history."
        actions={
          <>
            <Link to="/config">
              <Button variant="outline">
                <Sliders className="size-4" /> Configuration
              </Button>
            </Link>
            <RefreshButton
              busy={config.isFetching || health.isFetching}
              onRefresh={() => {
                void config.refetch();
                void health.refetch();
              }}
            />
          </>
        }
      />

      <Section eyebrow="build">
        {!config.data ? (
          <Skeleton className="h-40 w-full" />
        ) : (
          <SettingsSection title="Build" description="The binary that is answering, and how long it has been up.">
            <dl className="grid grid-cols-1 gap-x-8 gap-y-2 md:grid-cols-2">
              <Row label="Version">
                <span className="break-all">{config.data.version.version}</span>
              </Row>
              <Row label="Commit">
                {/* A full 40-char hash is wider than the 390 column and took
                    the document to 496px; the short hash is the readable one,
                    the full value stays in the tooltip. */}
                <span className="m-data" title={config.data.version.commit}>
                  {config.data.version.commit.slice(0, 7)}
                </span>
              </Row>
              <Row label="Built">{config.data.version.build_date}</Row>
              <Row label="Uptime">{duration(config.data.uptime_seconds)}</Row>
              <Row label="Database">
                <span className="break-all">{config.data.db_path}</span>{" "}
                <span className="m-data text-[var(--text-3)]">{bytes(config.data.db_size_bytes)}</span>
              </Row>
              <Row label="Config file">
                <span className="break-all">{config.data.config_path}</span>
              </Row>
            </dl>
          </SettingsSection>
        )}
      </Section>

      <Section
        eyebrow="health"
        aside={health.data ? `verdict ${health.data.status} · ${health.data.duration_ms} ms` : undefined}
      >
        {healthError ? (
          <ErrorState
            code={healthError.code}
            title="The health check did not answer"
            description={healthError.message}
            actions={<Button onClick={() => void health.refetch()}>Try again</Button>}
          />
        ) : !health.data ? (
          <Skeleton className="h-40 w-full" />
        ) : (
          <Card>
            <CardContent className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="m-label">doctor · role {health.data.role}</span>
                <StatusBadge status={CHECK_STATUS[health.data.status] ?? "neutral"}>
                  {health.data.status}
                </StatusBadge>
              </div>
              <ul className="flex flex-col">
                {health.data.checks.map((c) => (
                  <CheckRow key={c.name} check={c} />
                ))}
              </ul>
              {health.data.last_encode ? (
                <p className="text-xs text-[var(--text-3)]">
                  last successful encode <Time value={health.data.last_encode * 1000} mode="both" /> ·{" "}
                  {health.data.queued} queued
                </p>
              ) : null}
            </CardContent>
          </Card>
        )}
      </Section>

      <Section
        eyebrow="configuration"
        aside={config.data ? config.data.config_path : undefined}
      >
        {config.data ? (
          <Card className="min-w-0 overflow-hidden">
            <CardContent className="min-w-0">
              <pre className="m-data max-h-[32rem] overflow-auto whitespace-pre text-[var(--text-2)]">
                {JSON.stringify(config.data.config, null, 2)}
              </pre>
            </CardContent>
          </Card>
        ) : (
          <Skeleton className="h-64 w-full" />
        )}
      </Section>
    </Page>
  );
}

function CheckRow({ check }: { check: HealthCheck }) {
  return (
    <li className="grid grid-cols-1 items-baseline gap-x-3 gap-y-1 border-b border-border py-2 last:border-0 sm:grid-cols-[9rem_1fr]">
      <StatusBadge status={CHECK_STATUS[check.status] ?? "neutral"} className="w-fit">
        {check.name}
      </StatusBadge>
      <div className="min-w-0">
        <p className="m-0 text-sm text-[var(--text-2)]">{check.detail || check.code || "—"}</p>
        {check.fix ? <p className="m-0 text-xs text-[var(--text-3)]">{check.fix}</p> : null}
      </div>
    </li>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[7.5rem_1fr] items-baseline gap-3">
      <dt className="m-label">{label}</dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  );
}
