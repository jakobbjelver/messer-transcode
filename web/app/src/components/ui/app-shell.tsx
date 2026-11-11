import type { ComponentType, ReactNode } from "react";

/**
 * The family shell. Presentational on purpose: it takes `active` and a `linkComponent` instead of
 * importing next/link or usePathname, so the same block works in Next, Vite and Astro.
 *
 * variant="sidebar" — 240px aside on desktop, topbar below md. variant="topbar" — 48px header.
 * variant="none" — children only, for immersive screens (a swipe deck, a player).
 *
 * `container` is the page's container stop (canon §16): the topbar's inner row is constrained to the
 * same width and gutter as the page content, so the wordmark lines up with the column below it. The
 * bar's own border still spans the viewport — full-bleed surface, contained content.
 */

export type NavItem = {
  href: string;
  label: string;
  icon?: ReactNode;
  badge?: ReactNode;
  external?: boolean;
};

export type ContainerStop = "default" | "narrow" | "wide" | "none";

type LinkProps = {
  href: string;
  className?: string;
  children: ReactNode;
  target?: string;
  rel?: string;
  "aria-current"?: "page";
};
type LinkLike = ComponentType<LinkProps>;

export type AppShellProps = {
  brand: { name: string; mark?: ReactNode; href?: string };
  nav?: NavItem[];
  active?: string;
  variant?: "sidebar" | "topbar" | "none";
  /** Page container stop; must match the page's PageContainer. Canon §16. */
  container?: ContainerStop;
  user?: { name: string; role?: string };
  actions?: ReactNode;
  footer?: ReactNode;
  linkComponent?: LinkLike;
  children: ReactNode;
};

const CONTAINER: Record<ContainerStop, string> = {
  default: "max-w-[var(--shell)]",
  narrow: "max-w-[var(--shell-narrow)]",
  wide: "max-w-[var(--shell-wide)]",
  none: "",
};

const rowBase =
  "relative flex h-8 shrink-0 items-center gap-2 px-3 text-sm no-underline transition-[background-color,color] duration-150";
const rowIdle = "text-[var(--text-2)] hover:bg-[var(--surface-2)] hover:text-[var(--foreground)]";
const rowActive =
  "bg-primary/10 text-[var(--foreground)] after:absolute after:inset-y-0 after:left-0 after:w-0.5 after:bg-primary";

function isActiveHref(href: string, active?: string) {
  if (!active) return false;
  if (href === "/") return active === "/";
  return active === href || active.startsWith(href + "/");
}

function initials(name: string) {
  return name
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

export function AppShell({
  brand,
  nav = [],
  active,
  variant = "sidebar",
  container = "default",
  user,
  actions,
  footer,
  linkComponent,
  children,
}: AppShellProps) {
  if (variant === "none") return <>{children}</>;

  const Link = (linkComponent ?? "a") as LinkLike;

  const brandBlock = (
    <Link href={brand.href ?? "/"} className="flex h-14 shrink-0 items-center gap-2 border-b border-border px-4 no-underline">
      {brand.mark ? <span className="shrink-0 [&_svg]:size-5">{brand.mark}</span> : null}
      <span className="truncate font-mono text-sm font-medium uppercase tracking-[0.1em] text-[var(--foreground)]">
        {brand.name}
      </span>
    </Link>
  );

  const navRow = (item: NavItem) => {
    const current = isActiveHref(item.href, active);
    return (
      <Link
        key={item.href}
        href={item.href}
        aria-current={current ? "page" : undefined}
        data-state-selected={current ? "" : undefined}
        target={item.external ? "_blank" : undefined}
        rel={item.external ? "noreferrer" : undefined}
        className={[rowBase, current ? rowActive : rowIdle].join(" ")}
      >
        {item.icon ? (
          <span className="shrink-0 [&_svg]:size-5" aria-hidden="true">
            {item.icon}
          </span>
        ) : null}
        <span className="flex-1 truncate">{item.label}</span>
        {item.badge ? <span className="ml-auto shrink-0">{item.badge}</span> : null}
      </Link>
    );
  };

  const userBlock = user ? (
    <div className="flex items-center gap-2 border-t border-border px-3 py-3">
      <span
        data-avatar=""
        className="grid size-6 shrink-0 place-items-center rounded-full bg-[var(--surface-2)] font-mono text-[10px] text-[var(--foreground)]"
      >
        {initials(user.name)}
      </span>
      <span className="min-w-0">
        <span className="block truncate text-sm">{user.name}</span>
        {user.role ? (
          <span className="block font-mono text-[10.5px] uppercase tracking-[0.1em] text-[var(--text-3)]">
            {user.role}
          </span>
        ) : null}
      </span>
    </div>
  ) : null;

  const sidebar = (
    <aside className="hidden w-60 shrink-0 flex-col border-r border-border bg-[var(--surface)] md:flex">
      {brandBlock}
      <nav aria-label="Main" className="flex-1 overflow-y-auto p-2">
        {nav.map(navRow)}
      </nav>
      {userBlock}
      {actions ? (
        <div className="flex items-center justify-end border-t border-border px-3 py-2">{actions}</div>
      ) : null}
      {footer ? (
        <div className="border-t border-border px-3 py-2 font-mono text-[10.5px] uppercase tracking-[0.1em] text-[var(--text-3)]">
          {footer}
        </div>
      ) : null}
    </aside>
  );

  const topbar = (
    <header
      className={[
        "h-12 shrink-0 border-b border-border bg-[var(--surface)]",
        variant === "sidebar" ? "md:hidden" : "",
      ].join(" ")}
    >
      <div
        data-shell-row=""
        className={[
          "mx-auto flex h-full w-full items-center gap-3 px-4 sm:gap-6 md:px-8",
          CONTAINER[container],
        ]
          .join(" ")
          .trim()}
      >
        <Link href={brand.href ?? "/"} className="flex shrink-0 items-center gap-2 no-underline">
          {brand.mark ? <span className="shrink-0 [&_svg]:size-5">{brand.mark}</span> : null}
          <span className="hidden font-mono text-sm font-medium uppercase tracking-[0.1em] text-[var(--foreground)] sm:block">
            {brand.name}
          </span>
        </Link>
        <nav aria-label="Main" className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
          {nav.map((item) => {
            const current = isActiveHref(item.href, active);
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={current ? "page" : undefined}
                data-state-selected={current ? "" : undefined}
                target={item.external ? "_blank" : undefined}
                rel={item.external ? "noreferrer" : undefined}
                className={[
                  "relative flex h-8 shrink-0 items-center gap-1.5 px-2 text-sm no-underline transition-[background-color,color] duration-150",
                  current
                    ? "bg-primary/10 text-[var(--foreground)] after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:bg-primary"
                    : rowIdle,
                ].join(" ")}
              >
                {item.icon ? (
                  <span className="shrink-0 [&_svg]:size-4" aria-hidden="true">
                    {item.icon}
                  </span>
                ) : null}
                <span>{item.label}</span>
              </Link>
            );
          })}
        </nav>
        {actions || user ? (
          <div className="ml-auto flex shrink-0 items-center gap-2">
            {actions}
            {user ? (
              <>
                <span
                  data-avatar=""
                  className="grid size-6 place-items-center rounded-full bg-[var(--surface-2)] font-mono text-[10px]"
                >
                  {initials(user.name)}
                </span>
                <span className="hidden text-sm sm:block">{user.name}</span>
              </>
            ) : null}
          </div>
        ) : null}
      </div>
    </header>
  );

  return (
    <div className="flex min-h-screen bg-[var(--background)] text-[var(--foreground)]">
      {variant === "sidebar" ? sidebar : null}
      <div className="flex min-w-0 flex-1 flex-col">
        {variant === "topbar" || variant === "sidebar" ? topbar : null}
        <main className="min-w-0 flex-1">{children}</main>
      </div>
    </div>
  );
}
