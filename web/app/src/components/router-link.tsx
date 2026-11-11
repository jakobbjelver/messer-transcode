import { Link } from "react-router-dom";

/**
 * AppShell's `linkComponent` takes `href` so the block stays framework-free;
 * react-router's Link takes `to`. This adapter is the whole difference.
 */
export function RouterLink({
  href,
  children,
  className,
  target,
  rel,
  "aria-current": ariaCurrent,
}: {
  href: string;
  children: React.ReactNode;
  className?: string;
  target?: string;
  rel?: string;
  "aria-current"?: "page";
}) {
  return (
    <Link
      to={href}
      className={className}
      target={target}
      rel={rel}
      aria-current={ariaCurrent}
      // A route change keeps the scroll position otherwise, which reads as a stuck page.
      onClick={() => window.scrollTo({ top: 0 })}
    >
      {children}
    </Link>
  );
}
