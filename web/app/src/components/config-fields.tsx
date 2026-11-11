import { createContext, useContext, type ReactNode } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { getPath, listText, parseList } from "@/lib/config";
import type { ConfigDoc } from "@/lib/types";

// ── the draft ─────────────────────────────────────────────────────────────
//
// One object holds the draft, one function writes to it, and every field is a
// path into it. The same pointer addresses a field here and a change in the
// confirmation dialog, so the two can never disagree about what "the 1080p
// rung" means.

type Draft = {
  doc: ConfigDoc;
  set: (path: string, value: unknown) => void;
};

const DraftContext = createContext<Draft | null>(null);

export function DraftProvider({ doc, set, children }: Draft & { children: ReactNode }) {
  return <DraftContext.Provider value={{ doc, set }}>{children}</DraftContext.Provider>;
}

/** The draft, for a component that builds its own row layout. */
export function useConfigDraft(): Draft {
  return useDraft();
}

function useDraft(): Draft {
  const ctx = useContext(DraftContext);
  if (!ctx) throw new Error("config fields must be inside a DraftProvider");
  return ctx;
}

/** One labelled control. The label is in the human voice (canon rule 5). */
export function Field({
  label,
  hint,
  children,
  wide,
}: {
  label: string;
  hint?: ReactNode;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <div className={`flex min-w-0 flex-col gap-1 ${wide ? "sm:col-span-2" : ""}`.trim()}>
      <span className="m-label">{label}</span>
      {children}
      {hint ? <span className="m-meta text-[var(--text-3)]">{hint}</span> : null}
    </div>
  );
}

/** A text field bound to a path. Empty means empty, not a default. */
export function TextField({
  path,
  label,
  hint,
  placeholder,
  mono = true,
  wide,
}: {
  path: string;
  label: string;
  hint?: ReactNode;
  placeholder?: string;
  mono?: boolean;
  wide?: boolean;
}) {
  const { doc, set } = useDraft();
  const value = getPath(doc, path);
  return (
    <Field label={label} hint={hint} wide={wide}>
      <Input
        aria-label={label}
        className={mono ? "m-data" : undefined}
        placeholder={placeholder}
        value={typeof value === "string" ? value : ""}
        onChange={(e) => set(path, e.target.value)}
      />
    </Field>
  );
}

/**
 * A number field. An emptied field is written as 0 rather than left as the
 * string "" — the schema's minimums are what catch a real mistake, and a
 * half-typed number is not one yet.
 */
export function NumberField({
  path,
  label,
  hint,
  step = 1,
  min,
  max,
  wide,
}: {
  path: string;
  label: string;
  hint?: ReactNode;
  step?: number;
  min?: number;
  max?: number;
  wide?: boolean;
}) {
  const { doc, set } = useDraft();
  const value = getPath(doc, path);
  return (
    <Field label={label} hint={hint} wide={wide}>
      <Input
        aria-label={label}
        type="number"
        step={step}
        min={min}
        max={max}
        className="m-data"
        value={typeof value === "number" ? String(value) : ""}
        onChange={(e) => set(path, e.target.value === "" ? 0 : Number(e.target.value))}
      />
    </Field>
  );
}

/** A boolean as the canon checkbox. */
export function ToggleField({ path, label, hint }: { path: string; label: string; hint?: ReactNode }) {
  const { doc, set } = useDraft();
  const value = getPath(doc, path);
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <Checkbox
        aria-label={label}
        checked={Boolean(value)}
        onChange={(e) => set(path, e.target.checked)}
        label={<span className="text-sm">{label}</span>}
      />
      {hint ? <span className="m-meta text-[var(--text-3)]">{hint}</span> : null}
    </div>
  );
}

/** A free list, edited as a comma list and stored as an array. */
export function ListField({
  path,
  label,
  hint,
  placeholder = "swe, eng",
}: {
  path: string;
  label: string;
  hint?: ReactNode;
  placeholder?: string;
}) {
  const { doc, set } = useDraft();
  const value = getPath(doc, path);
  const items = Array.isArray(value) ? (value as string[]) : undefined;
  return (
    <Field label={label} hint={hint}>
      <Input
        aria-label={label}
        className="m-data"
        placeholder={placeholder}
        value={listText(items)}
        onChange={(e) => {
          const next = parseList(e.target.value);
          // An empty list is "not configured", which the schema spells as an
          // absent key rather than an empty array.
          set(path, next.length === 0 ? undefined : next);
        }}
      />
    </Field>
  );
}

/** A bare select for a row that already has a column label. */
export function SelectFieldRaw({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (v: string) => void;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label={label} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o} value={o}>
            {o}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

export function SelectField({
  path,
  label,
  hint,
  options,
  wide,
}: {
  path: string;
  label: string;
  hint?: ReactNode;
  options: { value: string; label: string }[];
  wide?: boolean;
}) {
  const { doc, set } = useDraft();
  const value = getPath(doc, path);
  return (
    <Field label={label} hint={hint} wide={wide}>
      <Select value={typeof value === "string" ? value : undefined} onValueChange={(v) => set(path, v)}>
        <SelectTrigger aria-label={label} className="w-full">
          <SelectValue placeholder="pick one" />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}
