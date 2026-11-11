import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

/**
 * The one text input. Canon rule 10 fixes the height at 32px, rule 5 keeps the
 * field label in the human voice (that is `Field`'s job, not the input's).
 *
 * Mirrors `field.tsx` in messer-request-games; vendored rather than promoted
 * because a registry item is compared byte-for-byte across every app, and
 * turning this into one is a messer-ui task, not a transcode one.
 */
export function Input({ className, ...props }: ComponentProps<"input">) {
  return (
    <input
      className={cn(
        "h-8 w-full border border-[var(--border-strong)] bg-[var(--surface)] px-2.5 text-sm text-[var(--foreground)]",
        "placeholder:text-[var(--text-3)] transition-colors duration-150",
        "focus-visible:border-[var(--ring)] disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}
