import type { ReactNode } from "react";

export type ErrorStateProps = {
  code: string;
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
};

/**
 * The 404 / 500 / "not allowed" screen. The code is the biggest thing on the page, in the machine
 * voice; the title says what happened and the actions say what to do next.
 */
export function ErrorState({ code, title, description, actions }: ErrorStateProps) {
  return (
    <div className="grid min-h-[60vh] place-items-center px-4 py-16 text-center">
      <div className="max-w-[52ch]">
        <p className="m-0 font-mono text-[64px] leading-none tabular-nums text-[var(--text-3)]">{code}</p>
        <h1 className="m-0 mt-4 text-2xl font-medium leading-tight">{title}</h1>
        {description ? <p className="m-0 mt-2 text-sm text-[var(--text-2)]">{description}</p> : null}
        {actions ? <div className="mt-6 flex flex-wrap items-center justify-center gap-2">{actions}</div> : null}
      </div>
    </div>
  );
}
