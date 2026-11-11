import { FilmSlate } from "@phosphor-icons/react";
import { useLocation } from "react-router-dom";

import { RouterLink } from "@/components/router-link";
import { ThemeControl } from "@/components/theme-control";
import { AppShell } from "@/components/ui/app-shell";
import { WindowBadge } from "@/components/window-badge";

const NAV = [
  { href: "/", label: "Overview" },
  { href: "/queue", label: "Queue" },
  { href: "/runs", label: "Runs" },
  { href: "/config", label: "Config" },
  { href: "/settings", label: "Settings" },
];

/** The single place this app knows its own navigation. */
export function AppChrome({ children }: { children: React.ReactNode }) {
  const { pathname } = useLocation();
  return (
    <AppShell
      variant="topbar"
      container="default"
      brand={{ name: "Messer/Transcode", mark: <FilmSlate weight="fill" /> }}
      nav={NAV}
      active={pathname}
      linkComponent={RouterLink}
      actions={
        <>
          {
            /* Supplementary: the badge is what leaves the bar first at 390, so
               the four nav items keep their room. */
          }
          <span className="hidden sm:block">
            <WindowBadge />
          </span>
          <ThemeControl />
        </>
      }
    >
      {children}
    </AppShell>
  );
}
