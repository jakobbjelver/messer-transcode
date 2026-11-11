import { ArrowRight, ClockCounterClockwise, Plus, Trash, WarningCircle } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

import {
  DraftProvider,
  Field,
  ListField,
  NumberField,
  SelectField,
  SelectFieldRaw,
  TextField,
  ToggleField,
  useConfigDraft,
} from "@/components/config-fields";
import { DiffDialog } from "@/components/diff-dialog";
import { Page } from "@/components/page";
import { RefreshButton } from "@/components/refresh-button";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ErrorState } from "@/components/ui/error-state";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, asApiError } from "@/lib/api";
import { deletePath, setPath } from "@/lib/config";
import { qk } from "@/lib/keys";
import type { ConfigDoc, Diff, DryRun } from "@/lib/types";

/**
 * The configuration, as something you can change.
 *
 * Three rules hold this screen together. The file on the volume is the truth and
 * this page only proposes; nothing is written until a diff has been read and
 * confirmed. History is append-only, so a save is a new revision and a revert is
 * a new revision that happens to contain old values. And every field here is
 * addressed by the same JSON pointer the diff uses, so what you change and what
 * you are shown are the same fact.
 */
export function ConfigPage() {
  const qc = useQueryClient();
  const config = useQuery({ queryKey: qk.config, queryFn: api.config });
  const history = useQuery({ queryKey: qk.configHistory(5), queryFn: () => api.configHistory(5) });

  const [base, setBase] = useState<ConfigDoc | null>(null);
  const [draft, setDraft] = useState<ConfigDoc | null>(null);
  const [docText, setDocText] = useState<string | null>(null);
  const [docError, setDocError] = useState<string | null>(null);
  const [review, setReview] = useState<DryRun | null>(null);
  const [conflict, setConflict] = useState<{ headRev: number } | null>(null);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [saveErr, setSaveErr] = useState<string | null>(null);
  const [restoreFor, setRestoreFor] = useState<number | null>(null);

  const headRev = config.data?.state.head_rev ?? 0;
  const seenRev = useRef<number | null>(null);
  useEffect(() => {
    if (!config.data) return;
    if (seenRev.current === headRev) return;
    seenRev.current = headRev;
    setBase(config.data.config);
    setDraft(config.data.config);
    setDocText(null);
    setDocError(null);
  }, [config.data, headRev]);

  const dryRun = useMutation({ mutationFn: (doc: ConfigDoc) => api.configDryRun(doc) });
  const adopt = useMutation({ mutationFn: (message: string) => api.configAdopt(message) });
  const revert = useMutation({ mutationFn: ({ rev, message }: { rev: number; message: string }) =>
    api.configRevert(rev, message) });

  const refresh = () => {
    void config.refetch();
    void history.refetch();
  };

  if (config.error) {
    const e = asApiError(config.error);
    return (
      <Page>
        <ErrorState
          code={e.code}
          title="The configuration did not load"
          description={e.message}
          actions={<Button onClick={() => void config.refetch()}>Try again</Button>}
        />
      </Page>
    );
  }

  if (!config.data || !draft || !base) {
    return (
      <Page>
        <div className="flex flex-col gap-3">
          <Skeleton className="h-8 w-80" />
          <Skeleton className="h-64 w-full" />
        </div>
      </Page>
    );
  }

  const state = config.data.state;
  const dirty = JSON.stringify(draft) !== JSON.stringify(base);

  const set = (path: string, value: unknown) => {
    setDraft((prev) => {
      if (!prev) return prev;
      return (value === undefined ? deletePath(prev, path) : setPath(prev, path, value)) as ConfigDoc;
    });
    if (docText !== null) setDocText(null); // the forms and the document are one draft
  };

  const startReview = () => {
    setSaveErr(null);
    setConflict(null);
    dryRun.mutate(draft, {
      onSuccess: (d) => {
        setReview(d);
        setReviewOpen(true);
      },
      onError: (e) => setSaveErr(asApiError(e).message),
    });
  };

  const confirmSave = async (message: string) => {
    setSaveErr(null);
    try {
      const res = await api.configSave(draft, review?.head_rev ?? headRev, message);
      setReviewOpen(false);
      setReview(null);
      setNotice(`Saved as revision ${res.rev}. ${res.warnings.join(" ")}`);
      await qc.invalidateQueries({ queryKey: qk.config });
      await qc.invalidateQueries({ queryKey: ["config", "history"] });
      void qc.invalidateQueries({ queryKey: qk.status });
      void history.refetch();
    } catch (e) {
      const err = asApiError(e);
      const payload = err.payload as { head_rev?: number; diff?: Diff } | undefined;
      if (err.status === 409 && payload) {
        setConflict({ headRev: payload.head_rev ?? 0 });
        setReview((prev) => (prev ? { ...prev, diff: payload.diff ?? prev.diff } : prev));
        return;
      }
      setReviewOpen(false);
      setSaveErr(err.message);
    }
  };

  const adoptDisk = async (message: string) => {
    try {
      const res = await adopt.mutateAsync(message);
      setReviewOpen(false);
      setNotice(`Adopted the file on disk as revision ${res.rev}.`);
      seenRev.current = res.rev;
      refresh();
    } catch (e) {
      setSaveErr(asApiError(e).message);
      setReviewOpen(false);
    }
  };

  return (
    <Page>
      <PageHeader
        eyebrow="config"
        title="The configuration, versioned"
        description={
          <>
            Revision <span className="m-data">{state.head_rev || "—"}</span> · {state.applies} ·{" "}
            <span className="m-data">{config.data.config_path}</span>
          </>
        }
        actions={
          <>
            <Link to="/config/history">
              <Button variant="outline">
                <ClockCounterClockwise className="size-4" /> History
              </Button>
            </Link>
            <RefreshButton busy={config.isFetching || history.isFetching} onRefresh={refresh} />
          </>
        }
      />

      {state.dirty ? (
        <DiskNotice
          state={state}
          headRev={state.head_rev}
          busy={adopt.isPending}
          onReview={async () => {
            const d = await api.configDiff(state.head_rev, 0);
            setConflict(null);
            setReview({ ok: true, problems: [], head_rev: state.head_rev, head_sha: state.head_sha, sha: "", state, diff: d });
            setReviewOpen(true);
          }}
          onRestore={() => setRestoreFor(state.head_rev)}
        />
      ) : null}

      {notice ? (
        <Notice tone="ok" onDismiss={() => setNotice(null)}>
          {notice}
        </Notice>
      ) : null}
      {saveErr ? (
        <Notice tone="bad" onDismiss={() => setSaveErr(null)}>
          {saveErr}
        </Notice>
      ) : null}

      <Tabs defaultValue="form" className="gap-4">
        <TabsList>
          <TabsTrigger value="form">Fields</TabsTrigger>
          <TabsTrigger value="document">Document</TabsTrigger>
        </TabsList>

        <TabsContent value="form" className="flex flex-col gap-6">
          <DraftProvider doc={draft} set={set}>
            <Section eyebrow="schedule" aside="when jobs may run">
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <TextField path="/schedule/weekday" label="Weekday window" placeholder="00:30-05:30" />
                <TextField path="/schedule/weekend" label="Weekend window" placeholder="02:00-07:00" />
                <NumberField
                  path="/schedule/drain_before_minutes"
                  label="Stop claiming, minutes before the window closes"
                  hint="a running job is never killed; this is when new work stops"
                />
                <TextField path="/schedule/timezone" label="Timezone" placeholder="Europe/Stockholm" />
              </div>
            </Section>

            <Section eyebrow="workers" aside="concurrency on the node">
              <div className="grid gap-4 sm:grid-cols-3">
                <NumberField path="/workers/encode" label="Encode workers" min={1} hint="one: the media engine is serial" />
                <NumberField path="/workers/probe" label="Probe workers" min={1} />
                <NumberField path="/workers/ingest_buffer" label="Ingest buffer" min={1} />
              </div>
            </Section>

            <Section eyebrow="paths" aside="node-local, absolute">
              <div className="grid gap-4 sm:grid-cols-2">
                <TextField path="/paths/scratch" label="Scratch" placeholder="/Volumes/Active/scratch" />
                <TextField path="/paths/quarantine" label="Quarantine" placeholder="/Volumes/Media/.quarantine" />
              </div>
            </Section>

            <Section eyebrow="quality" aside="the bitrate ladder">
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <SelectField
                  path="/quality/mode"
                  label="Mode"
                  options={[
                    { value: "table", label: "table" },
                    { value: "probe", label: "probe" },
                    { value: "fixed", label: "fixed" },
                  ]}
                />
                <NumberField path="/quality/vmaf_target" label="VMAF target" step={0.5} />
                <NumberField path="/quality/vmaf_tolerance" label="VMAF tolerance" step={0.1} />
                <NumberField path="/quality/min_saving_pct" label="Smallest saving worth an encode" hint="absent means 10%" />
              </div>
              <div className="mt-4 grid gap-4 sm:grid-cols-2">
                <NumberField path="/quality/probe_outliers_above_kbps" label="Probe outliers above (kbps)" step={100} />
              </div>
              <Ladder />
            </Section>

            <Section eyebrow="audio" aside="track retention">
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <ListField path="/audio/keep_languages" label="Keep languages" placeholder="swe, eng" />
                <NumberField path="/audio/max_tracks" label="Maximum tracks" min={1} />
                <ToggleField path="/audio/keep_original_language" label="Keep the original language" />
                <ToggleField path="/audio/drop_commentary" label="Drop commentary" />
              </div>
              <AudioTranscodeRows />
            </Section>

            <Section eyebrow="subtitles" aside="text tracks">
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <ListField path="/subtitles/keep_languages" label="Keep languages" placeholder="swe, eng" />
                <ToggleField path="/subtitles/keep_forced" label="Keep forced subtitles" />
                <ToggleField path="/subtitles/drop_commentary" label="Drop commentary" />
                <ToggleField path="/subtitles/drop_closed_captions" label="Drop closed captions" />
              </div>
            </Section>

            <Section eyebrow="safety" aside="what keeps a bad decision from costing data">
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <NumberField path="/safety/min_free_gb" label="Disk floor (GB)" min={1} />
                <NumberField path="/safety/size_ratio_min" label="Smallest allowed ratio" step={0.01} />
                <NumberField path="/safety/size_ratio_max" label="Largest allowed growth" step={0.01} hint="a re-encode that grows is refused" />
                <NumberField path="/safety/duration_tolerance_s" label="Duration tolerance (s)" />
                <NumberField path="/safety/keep_quarantine_days" label="Quarantine keep (days)" />
                <NumberField path="/safety/verify_sample_seconds" label="Verification sample (s)" />
                <NumberField path="/safety/max_attempts" label="Attempt budget" min={1} />
                <SelectField
                  path="/safety/quarantine"
                  label="Original"
                  options={[
                    { value: "copy", label: "copy it first" },
                    { value: "off", label: "replace in place" },
                  ]}
                />
              </div>
              <div className="mt-4">
                <ToggleField path="/safety/verify_vmaf" label="Verify VMAF after an encode" />
              </div>
            </Section>
          </DraftProvider>

          <Section eyebrow="not on this screen" aside="edit in the document tab">
            <ul className="flex flex-col divide-y rounded-lg border text-sm">
              {[
                { key: "rules", label: "Rules", detail: `${draft.rules.length} in order — order is the meaning` },
                { key: "libraries", label: "Libraries", detail: draft.libraries.map((l) => `${l.name} → ${l.root}`).join(" · ") },
                { key: "notify", label: "Notify", detail: draft.notify.radarr || draft.notify.sonarr || "no *arr endpoints" },
                { key: "server", label: "Server", detail: `${draft.server.listen} — the token lives in a file, not here` },
              ].map((row) => (
                <li key={row.key} className="flex flex-wrap items-baseline justify-between gap-2 px-3 py-2">
                  <span>{row.label}</span>
                  <span className="m-data text-[var(--text-3)]">{row.detail}</span>
                </li>
              ))}
            </ul>
          </Section>
        </TabsContent>

        <TabsContent value="document">
          <DocumentEditor
            draft={draft}
            text={docText ?? JSON.stringify(draft, null, 2)}
            error={docError}
            onChange={(text) => {
              setDocText(text);
              try {
                const parsed = JSON.parse(text) as ConfigDoc;
                setDocError(null);
                setDraft(parsed);
              } catch (e) {
                setDocError(e instanceof Error ? e.message : "not JSON");
              }
            }}
            onReset={() => {
              setDocText(null);
              setDocError(null);
              setDraft(base);
            }}
          />
        </TabsContent>
      </Tabs>

      {dirty ? (
        <div className="sticky bottom-4 z-10 flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card px-4 py-3 shadow-lg">
          <span className="text-sm">
            Unsaved changes to the draft — nothing has been written.
          </span>
          <span className="flex gap-2">
            <Button
              variant="ghost"
              onClick={() => {
                setDraft(base);
                setDocText(null);
                setDocError(null);
              }}
            >
              Discard
            </Button>
            <Button onClick={startReview} disabled={dryRun.isPending} aria-busy={dryRun.isPending}>
              {dryRun.isPending ? "Checking…" : "Review and save"}
            </Button>
          </span>
        </div>
      ) : null}

      <DiffDialog
        open={reviewOpen}
        onOpenChange={(v) => {
          setReviewOpen(false);
          if (!v) setConflict(null);
        }}
        diff={review?.diff ?? null}
        problems={review?.problems ?? []}
        busy={adopt.isPending}
        conflict={conflict}
        title={state.dirty ? "What changed on the volume" : "Review this change"}
        confirmLabel={state.dirty ? "Adopt into history" : "Save it"}
        onConfirm={state.dirty ? adoptDisk : confirmSave}
      />

      <RestoreDialog
        rev={restoreFor}
        onClose={() => setRestoreFor(null)}
        onConfirm={async (message) => {
          if (restoreFor === null) return;
          try {
            const res = await revert.mutateAsync({ rev: restoreFor, message });
            setRestoreFor(null);
            setNotice(`Revision ${restoreFor} restored as revision ${res.rev}.`);
            seenRev.current = res.rev;
            refresh();
          } catch (e) {
            setRestoreFor(null);
            setSaveErr(asApiError(e).message);
          }
        }}
        busy={revert.isPending}
      />
    </Page>
  );
}

// ── the ladder ────────────────────────────────────────────────────────────

function Ladder() {
  return <RowsEditor kind="ladder" />;
}

function AudioTranscodeRows() {
  return <RowsEditor kind="audio" />;
}

/**
 * The two list editors on this screen. They share a shape — a row of inputs, a
 * remove button, an add button — because a list that grows is a list a person
 * will add to at 23:00, and it should not need a JSON tab for that.
 */
function RowsEditor({ kind }: { kind: "ladder" | "audio" }) {
  const { doc, set } = useConfigDraft();
  const rows: Record<string, unknown>[] =
    kind === "ladder" ? (doc.quality.table as unknown as Record<string, unknown>[]) : ((doc.audio.transcode ?? []) as unknown as Record<string, unknown>[]);
  const path = kind === "ladder" ? "/quality/table" : "/audio/transcode";

  const update = (i: number, key: string, value: unknown) => {
    set(`${path}/${i}/${key}`, value);
  };

  const add = () => {
    const row =
      kind === "ladder"
        ? { codec: "h264", height_gte: 1080, kbps: 6000 }
        : { from: ["truehd"], to: "eac3", kbps: 640 };
    set(`${path}/${rows.length}`, row);
  };

  const remove = (i: number) => {
    set(`${path}/${i}`, undefined);
  };

  return (
    <div className="mt-4 flex flex-col gap-2">
      <div className="m-label">{kind === "ladder" ? "the ladder" : "audio conversion"}</div>
      <div className="flex flex-col gap-2">
        {rows.length === 0 ? (
          <p className="m-meta text-[var(--text-3)]">none — nothing is {kind === "ladder" ? "encoded" : "converted"}</p>
        ) : null}
        {rows.map((row, i) => (
          <div key={i} className="grid grid-cols-[1fr_auto] items-end gap-2 border-b pb-2 last:border-b-0">
            <div className="grid gap-2 sm:grid-cols-4">
              {kind === "ladder" ? (
                <>
                  <Field label={i === 0 ? "codec" : ""}>
                    <Input
                      aria-label={`row ${i + 1} codec`}
                      className="m-data"
                      value={String(row.codec ?? "")}
                      onChange={(e) => update(i, "codec", e.target.value)}
                    />
                  </Field>
                  <Field label={i === 0 ? "from height" : ""}>
                    <Input
                      aria-label={`row ${i + 1} height`}
                      type="number"
                      className="m-data"
                      value={String(row.height_gte ?? "")}
                      onChange={(e) => update(i, "height_gte", Number(e.target.value))}
                    />
                  </Field>
                  <Field label={i === 0 ? "kbps" : ""}>
                    <Input
                      aria-label={`row ${i + 1} kbps`}
                      type="number"
                      className="m-data"
                      value={String(row.kbps ?? "")}
                      onChange={(e) => update(i, "kbps", Number(e.target.value))}
                    />
                  </Field>
                  <Field label={i === 0 ? "expected VMAF" : ""}>
                    <Input
                      aria-label={`row ${i + 1} expected vmaf`}
                      type="number"
                      step={0.1}
                      className="m-data"
                      value={row.expected_vmaf === undefined ? "" : String(row.expected_vmaf)}
                      onChange={(e) =>
                        update(i, "expected_vmaf", e.target.value === "" ? undefined : Number(e.target.value))
                      }
                    />
                  </Field>
                </>
              ) : (
                <>
                  <Field label={i === 0 ? "from codecs" : ""}>
                    <Input
                      aria-label={`row ${i + 1} from`}
                      className="m-data"
                      value={((row.from as string[]) ?? []).join(", ")}
                      onChange={(e) =>
                        update(i, "from", e.target.value.split(",").map((s) => s.trim()).filter(Boolean))
                      }
                    />
                  </Field>
                  <Field label={i === 0 ? "to" : ""}>
                    <SelectFieldRaw
                      value={String(row.to ?? "eac3")}
                      options={["eac3", "ac3", "aac", "opus"]}
                      onChange={(v) => update(i, "to", v)}
                      label={`row ${i + 1} target codec`}
                    />
                  </Field>
                  <Field label={i === 0 ? "kbps" : ""}>
                    <Input
                      aria-label={`row ${i + 1} kbps`}
                      type="number"
                      className="m-data"
                      value={String(row.kbps ?? "")}
                      onChange={(e) => update(i, "kbps", Number(e.target.value))}
                    />
                  </Field>
                </>
              )}
            </div>
            <Button variant="ghost" size="icon" aria-label="Remove this row" onClick={() => remove(i)}>
              <Trash className="size-4" />
            </Button>
          </div>
        ))}
      </div>
      <div>
        <Button variant="outline" size="sm" onClick={add}>
          <Plus className="size-4" /> {kind === "ladder" ? "Add a rung" : "Add a conversion"}
        </Button>
      </div>
    </div>
  );
}

// ── small pieces ──────────────────────────────────────────────────────────

function DocumentEditor({
  text,
  error,
  onChange,
  onReset,
}: {
  draft: ConfigDoc;
  text: string;
  error: string | null;
  onChange: (text: string) => void;
  onReset: () => void;
}) {
  return (
    <Section eyebrow="document" aside={error ? <span className="text-destructive">not JSON</span> : "every key"}>
      <div className="flex flex-col gap-2">
        <textarea
          aria-label="The configuration document"
          spellCheck={false}
          className="m-data h-[60vh] w-full resize-y border border-[var(--border-strong)] bg-[var(--surface)] p-3 text-xs leading-relaxed"
          value={text}
          onChange={(e) => onChange(e.target.value)}
        />
        <div className="flex items-center justify-between gap-3">
          <span className={`m-meta ${error ? "text-destructive" : "text-[var(--text-3)]"}`}>
            {error ?? "valid JSON — the draft is what this says"}
          </span>
          <Button variant="ghost" size="sm" onClick={onReset}>
            Reset to revision
          </Button>
        </div>
      </div>
    </Section>
  );
}

function DiskNotice({
  state,
  headRev,
  busy,
  onReview,
  onRestore,
}: {
  state: { dirty: string; disk_error?: string };
  headRev: number;
  busy: boolean;
  onReview: () => void;
  onRestore: () => void;
}) {
  const invalid = state.dirty === "invalid";
  return (
    <Card className="border-warning/40">
      <CardContent className="flex flex-wrap items-start justify-between gap-3 py-1">
        <div className="flex gap-3">
          <WarningCircle className="mt-0.5 size-5 shrink-0 text-[var(--text-2)]" />
          <div className="flex flex-col gap-1">
            <span className="text-sm font-medium">
              {invalid
                ? "The file on the volume no longer loads"
                : state.dirty === "absent"
                  ? "There is no configuration file on the volume"
                  : "The file on the volume was edited by hand"}
            </span>
            <span className="m-meta text-[var(--text-3)]">
              {invalid
                ? state.disk_error
                : `It differs from revision ${headRev}. The server already runs it; history does not know about it yet.`}
            </span>
          </div>
        </div>
        <div className="flex gap-2">
          {!invalid && state.dirty !== "absent" ? (
            <Button variant="outline" size="sm" onClick={onReview} disabled={busy}>
              See what changed <ArrowRight className="size-4" />
            </Button>
          ) : null}
          <Button variant="ghost" size="sm" onClick={onRestore}>
            Restore revision {headRev}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function RestoreDialog({
  rev,
  onClose,
  onConfirm,
  busy,
}: {
  rev: number | null;
  onClose: () => void;
  onConfirm: (message: string) => void;
  busy: boolean;
}) {
  const [message, setMessage] = useState("");
  return (
    <Dialog open={rev !== null} onOpenChange={(v) => (v ? null : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Restore revision {rev}?</DialogTitle>
          <DialogDescription>
            This writes the old document as a new revision. Nothing is deleted: the revisions in between stay in the
            history, and this one can be restored in turn.
          </DialogDescription>
        </DialogHeader>
        <DialogBody>
          <Input
            aria-label="Why restore"
            placeholder="Why restore it?"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
          />
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => onConfirm(message.trim())} disabled={busy} aria-busy={busy}>
            {busy ? "Restoring…" : `Restore ${rev}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Notice({
  tone,
  children,
  onDismiss,
}: {
  tone: "ok" | "bad";
  children: React.ReactNode;
  onDismiss: () => void;
}) {
  return (
    <div
      className={`flex items-start justify-between gap-3 rounded-lg border px-3 py-2 text-sm ${tone === "bad" ? "border-destructive/40 bg-destructive/5" : ""}`}
    >
      <span className="flex items-center gap-2">
        <StatusBadge status={tone === "bad" ? "danger" : "success"}>{tone === "bad" ? "not saved" : "saved"}</StatusBadge>
        {children}
      </span>
      <button type="button" className="m-meta cursor-pointer text-[var(--text-3)] hover:text-foreground" onClick={onDismiss}>
        dismiss
      </button>
    </div>
  );
}
