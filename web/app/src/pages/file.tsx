import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router-dom";

import { BackButton } from "@/components/back-button";
import { Page } from "@/components/page";
import { Section } from "@/components/section";
import { Card, CardContent } from "@/components/ui/card";
import { ErrorState } from "@/components/ui/error-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Time } from "@/components/ui/time";
import { api, asApiError } from "@/lib/api";
import { bytes, duration, fileName, gb, kbps } from "@/lib/format";
import { qk } from "@/lib/keys";
import { statePresentation } from "@/lib/status";

export function FilePage() {
  const { id } = useParams();
  const fileID = Number(id);
  const valid = Number.isFinite(fileID) && fileID > 0;
  const detail = useQuery({ queryKey: qk.file(fileID), queryFn: () => api.file(fileID), enabled: valid });
  const detailError = detail.error ? asApiError(detail.error) : null;

  // The origin comes from the link that opened this page — the queue's filter,
  // the dashboard or a run — so the label is never a guess.
  const back = <BackButton parent="/queue" />;

  if (!valid) {
    return (
      <Page>
        <ErrorState code="E_BAD_ID" title="That is not a file" description="The address carries no file id." actions={back} />
      </Page>
    );
  }

  if (detailError) {
    return (
      <Page>
        <ErrorState
          code={detailError.code}
          title={detailError.status === 404 ? "No such file" : "The file did not load"}
          description={detailError.message}
          actions={back}
        />
      </Page>
    );
  }

  if (!detail.data) {
    return (
      <Page>
        <div className="flex flex-col gap-3">
          <Skeleton className="h-8 w-96" />
          <Skeleton className="h-64 w-full" />
        </div>
      </Page>
    );
  }

  const file = detail.data.file;
  const presentation = statePresentation(file.state);
  const expected = detail.data.expected_vmaf;

  return (
    <Page>
      <PageHeader
        eyebrow={`${file.library || "library"} · ${file.decision || "no decision"}`}
        title={fileName(file.path)}
        description={<span className="break-all">{file.path}</span>}
        actions={
          <>
            <StatusBadge status={presentation.status}>{presentation.label}</StatusBadge>
            {back}
          </>
        }
      />

      <Section eyebrow="the file">
        <Card>
          <CardContent>
            <dl className="grid grid-cols-1 gap-x-8 gap-y-3 md:grid-cols-2">
              <Row label="Decision">
                {file.decision || "—"}{" "}
                <span className="m-data text-[var(--text-2)]">{file.reason_code}</span>
              </Row>
              <Row label="Why">{file.reason || "—"}</Row>
              <Row label="Source">
                <span className="m-data">
                  {file.video_codec || "?"} · {file.width}×{file.height} ·{" "}
                  {detail.data.nominal_height}p class
                </span>{" "}
                <span className="m-data text-[var(--text-3)]">{kbps(file.bitrate_kbps)}</span>
              </Row>
              <Row label="Target">
                <span className="m-data">{file.target_kbps ? `${file.target_kbps} kbps` : "—"}</span>
              </Row>
              <Row label="Duration">
                <span className="m-data">{duration(file.duration_s)}</span>
              </Row>
              <Row label="Size now">
                <span className="m-data">{bytes(file.size_bytes)}</span>
              </Row>
              {file.bytes_after > 0 ? (
                <Row label="Size after">
                  <span className="m-data">{bytes(file.bytes_after)}</span>{" "}
                  <span className="m-data text-[var(--text-3)]">from {bytes(file.bytes_before)}</span>
                </Row>
              ) : null}
              {file.vmaf > 0 ? (
                <Row label="VMAF">
                  <span className="m-data">{file.vmaf.toFixed(1)}</span>{" "}
                  <span className="m-data text-[var(--text-3)]">
                    expected {expected.toFixed(1)}
                  </span>
                </Row>
              ) : null}
              <Row label="Reclaim">
                <span className="m-data">{gb(detail.data.reclaim_bytes)} GB</span>
              </Row>
              <Row label="Attempts">
                <span className="m-data">{file.attempts}</span>
              </Row>
              <Row label="Audio">{detail.data.audio_list}</Row>
              <Row label="Subtitles">{detail.data.sub_list}</Row>
              <Row label="Updated">
                {file.last_error_at ? <Time value={file.last_error_at * 1000} mode="both" /> : "—"}
              </Row>
            </dl>
          </CardContent>
        </Card>
      </Section>

      {file.last_error ? (
        <Section eyebrow="last error" aside={file.attempts ? `${file.attempts} attempt(s)` : undefined}>
          <Card>
            <CardContent>
              <pre className="m-data overflow-x-auto whitespace-pre-wrap break-words text-[var(--text-2)]">
                {file.last_error}
              </pre>
            </CardContent>
          </Card>
        </Section>
      ) : null}
    </Page>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[7.5rem_1fr] items-baseline gap-3 border-b border-border pb-2 last:border-0">
      <dt className="m-label">{label}</dt>
      <dd className="min-w-0 text-sm text-[var(--foreground)]">{children}</dd>
    </div>
  );
}
