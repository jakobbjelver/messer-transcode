import { Moon, Sun } from "@phosphor-icons/react";

/**
 * The family theme toggle (canon §16 shell contract): a 32 px outlined icon button in AppShell's
 * `actions` slot. Presentational on purpose — pass `dark` and `onToggle`, keep the storage hook in
 * the app (next-themes in Next, a 20-line `useTheme` in Vite), so one block renders identically in
 * every port and no framework import leaks into the registry.
 *
 * The icon swap is attribute-driven (`data-dark`), not a `dark:` variant: apps differ in whether they
 * declare `@custom-variant dark`, and a control that silently loses its icon in one app is worse than
 * no animation at all.
 *
 * The glyph is 16 px (`size-4`), the same glyph the generated fronts' 32 px `.pg-icon-btn` draws. The
 * block used 1.2rem (19.2 px) until 2026-11-13, which made the toggle read a size heavier in every
 * React app than on ui.messer.wtf / projects.messer.wtf; the smaller glyph is the family size.
 */
export function ThemeToggle({
  dark,
  onToggle,
  label,
}: {
  dark: boolean;
  onToggle: () => void;
  /** Overrides the accessible name; defaults to the standard switch copy. */
  label?: string;
}) {
  const text = label ?? (dark ? "Switch to light theme" : "Switch to dark theme");

  return (
    <button
      type="button"
      data-slot="theme-toggle"
      data-dark={dark ? "" : undefined}
      aria-label={text}
      title={text}
      onClick={onToggle}
      className="group/theme relative grid size-8 shrink-0 cursor-pointer place-items-center border border-border bg-[var(--surface)] text-[var(--foreground)] transition-[background-color,border-color,color] duration-150 hover:bg-[var(--surface-2)]"
    >
      <Sun
        aria-hidden="true"
        className="size-4 scale-100 rotate-0 transition-transform duration-150 group-data-[dark]/theme:scale-0 group-data-[dark]/theme:-rotate-90"
      />
      <Moon
        aria-hidden="true"
        className="absolute size-4 scale-0 rotate-90 transition-transform duration-150 group-data-[dark]/theme:scale-100 group-data-[dark]/theme:rotate-0"
      />
    </button>
  );
}
