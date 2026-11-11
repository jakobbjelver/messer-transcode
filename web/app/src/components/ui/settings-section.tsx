"use client";

import { useState, type ReactNode } from "react";

export type SettingsSectionProps = {
  title: string;
  description?: ReactNode;
  children: ReactNode;
  onSave?: () => void | Promise<void>;
  dirty?: boolean;
  footer?: ReactNode;
};

/**
 * One block of a settings page: title, description, fields, and — only when the
 * section is editable — a save bar that sticks to the bottom while it scrolls.
 * The parent owns `dirty`; the section owns the pending state. A read-only
 * section (no `onSave`) renders no bar: a permanently disabled Save button is a
 * promise the surface cannot keep.
 */
export function SettingsSection({
  title,
  description,
  children,
  onSave,
  dirty = false,
  footer,
}: SettingsSectionProps) {
  const [pending, setPending] = useState(false);

  async function save() {
    if (!onSave || pending) return;
    setPending(true);
    try {
      await onSave();
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="grid gap-4 border border-border bg-[var(--surface)]">
      <div className="grid gap-1 px-4 pt-4">
        <h2 className="m-0 text-base font-medium leading-tight">{title}</h2>
        {description ? <p className="m-0 text-sm text-[var(--text-2)]">{description}</p> : null}
      </div>

      <div className="px-4 pb-4">{children}</div>

      {footer ??
        (onSave ? (
          <div className="sticky bottom-0 flex items-center justify-end gap-2 border-t border-border bg-[var(--background)] px-4 py-3">
            <span
              className="mr-auto font-mono text-[10.5px] uppercase tracking-[0.1em] text-[var(--text-3)]"
              aria-live="polite"
            >
              {dirty ? "Unsaved changes" : "Saved"}
            </span>
            <button
              type="button"
              onClick={save}
              disabled={!dirty || pending}
              className="inline-flex h-8 items-center justify-center bg-primary px-3 text-sm font-medium text-[var(--primary-foreground)] transition-opacity duration-150 hover:opacity-90 disabled:opacity-50"
            >
              {pending ? "Saving…" : "Save changes"}
            </button>
          </div>
        ) : null)}
    </section>
  );
}
