import { CaretRight } from "@phosphor-icons/react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { byGroup, showValue, summaryLine } from "@/lib/config";
import type { Change, Diff, Problem } from "@/lib/types";

/**
 * What a save would change, and the confirmation that goes with it.
 *
 * The primary reading is structural — one row per changed leaf, grouped by the
 * section of the document it lives in — because that is what a person is
 * deciding about. The unified text diff is one click away for the cases where
 * the raw shape matters.
 */
export function DiffDialog({
  open,
  onOpenChange,
  diff,
  problems,
  busy,
  conflict,
  title = "Review this change",
  confirmLabel = "Save it",
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  diff: Diff | null;
  problems: Problem[];
  busy?: boolean;
  conflict?: { headRev: number } | null;
  title?: string;
  confirmLabel?: string;
  onConfirm: (message: string) => void;
}) {
  const [message, setMessage] = useState("");
  const [raw, setRaw] = useState(false);
  const blocked = problems.length > 0 || !!conflict;
  const empty = diff ? summaryLine(diff) === "nothing changes" : true;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{conflict ? "Someone else saved first" : title}</DialogTitle>
          <DialogDescription>
            {conflict
              ? `This draft was built from an earlier revision. Revision ${conflict.headRev} is now the newest — review what it changed, then start from it.`
              : problems.length > 0
                ? "This draft breaks a rule. Nothing will be written until it is fixed."
                : diff
                  ? summaryLine(diff)
                  : "Checking the draft…"}
          </DialogDescription>
        </DialogHeader>

        <DialogBody className="flex flex-col gap-4">
          {problems.length > 0 ? (
            <ul className="flex flex-col gap-1.5 rounded-lg border border-destructive/40 bg-destructive/5 p-3">
              {problems.map((p) => (
                <li key={p.path + p.message} className="flex gap-2 text-sm">
                  <span className="m-data shrink-0 text-[var(--text-3)]">{p.path}</span>
                  <span>{p.message}</span>
                </li>
              ))}
            </ul>
          ) : null}

          {diff && diff.changes.length > 0 ? (
            raw ? (
              <pre className="m-data max-h-[40vh] overflow-auto rounded-lg border bg-[var(--surface)] p-3 text-xs leading-relaxed whitespace-pre">
                {diff.text}
              </pre>
            ) : (
              <div className="flex flex-col gap-4">
                {byGroup(diff.changes).map((g) => (
                  <div key={g.key} className="flex flex-col gap-1">
                    <div className="m-eyebrow flex items-baseline gap-2 text-[var(--text-3)]">
                      {g.label}
                      <span className="m-data">{g.changes.length}</span>
                    </div>
                    <ul className="flex flex-col divide-y rounded-lg border">
                      {g.changes.map((c) => (
                        <ChangeRow key={c.path + c.kind} change={c} />
                      ))}
                    </ul>
                  </div>
                ))}
              </div>
            )
          ) : empty ? (
            <p className="text-sm text-muted-foreground">
              Nothing would change. The file on the volume already matches this draft.
            </p>
          ) : null}
        </DialogBody>

        <DialogFooter>
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            {!conflict ? (
              <Input
                aria-label="Why this change"
                placeholder="Why this change? (kept in the history)"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                disabled={blocked}
              />
            ) : null}
            {diff && diff.changes.length > 0 ? (
              <button
                type="button"
                onClick={() => setRaw((v) => !v)}
                className="m-meta flex w-fit cursor-pointer items-center gap-1 text-[var(--text-3)] hover:text-foreground"
              >
                <CaretRight className={`size-3 transition-transform ${raw ? "rotate-90" : ""}`} />
                {raw ? "read it as a list of changes" : "read the raw diff"}
              </button>
            ) : null}
          </div>
          <div className="flex shrink-0 gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              onClick={() => onConfirm(message.trim())}
              disabled={busy || blocked || empty}
              aria-busy={busy}
            >
              {busy ? "Saving…" : confirmLabel}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const KIND = {
  add: { label: "added", className: "text-[var(--text-2)]" },
  remove: { label: "removed", className: "text-[var(--text-3)] line-through decoration-1" },
  change: { label: "changed", className: "text-foreground" },
  reorder: { label: "reordered", className: "text-[var(--text-2)]" },
} as const;

function ChangeRow({ change }: { change: Change }) {
  const kind = KIND[change.kind];
  return (
    <li className="flex flex-col gap-0.5 px-3 py-2 text-sm">
      <span className="m-meta text-[var(--text-3)]">{change.label}</span>
      <span className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        {change.kind !== "add" ? (
          <span className={`m-data ${kind.className}`}>{showValue(change.before)}</span>
        ) : null}
        {change.kind === "change" ? <span className="text-[var(--text-3)]">→</span> : null}
        {change.kind !== "remove" ? (
          <span className="m-data font-medium">{showValue(change.after)}</span>
        ) : null}
        {change.kind === "reorder" ? <span className="m-meta text-[var(--text-3)]">{kind.label}</span> : null}
      </span>
    </li>
  );
}
