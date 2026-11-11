import { createContext, useContext, useEffect, useRef } from "react";

import { useStream } from "@/hooks/use-stream";
import { qk, REV_INVALIDATED } from "@/lib/keys";
import { queryClient } from "@/lib/query";
import type { StatusResponse, StreamPulse } from "@/lib/types";

type StreamValue = { pulse: StreamPulse | null; connected: boolean; rev: number };

const StreamContext = createContext<StreamValue>({ pulse: null, connected: false, rev: 0 });

/**
 * One EventSource for the whole app, and the cache's only invalidation signal.
 *
 * The shell's status badge, the overview's live figures and every list share
 * it: two connections would be two truths and two reconnects. The pulse is also
 * where the caching decision pays off — it carries `rev` (the newest event id),
 * so "the queue changed" is one integer comparison instead of each screen
 * polling its own endpoint.
 *
 * The `/ui` sink mounts this provider but opens no stream: the sink measures
 * static style, and an open SSE connection never lets a harness see the page
 * idle — it fails the run with a navigation timeout rather than a style finding.
 */
export function StreamProvider({ children }: { children: React.ReactNode }) {
  const enabled = typeof window === "undefined" || window.location.pathname !== "/ui";
  const { pulse, connected } = useStream(enabled);
  const lastRev = useRef(0);

  useEffect(() => {
    if (!pulse) return;

    // The live numbers arrive *inside* the pulse, so they are written into the
    // status cache rather than invalidated: a refetch would race the next pulse.
    queryClient.setQueryData<StatusResponse>(qk.status, (old) =>
      old
        ? {
            ...old,
            rev: pulse.rev,
            window: pulse.window,
            dashboard: pulse.dashboard ?? old.dashboard,
            live: pulse.live,
          }
        : old,
    );

    if (lastRev.current === 0) {
      // First pulse after connecting: the mounting screens have just fetched,
      // so recording the revision is enough. Invalidating here would double
      // every first page load at 1 Hz.
      lastRev.current = pulse.rev;
      return;
    }
    if (pulse.rev > lastRev.current) {
      lastRev.current = pulse.rev;
      for (const key of REV_INVALIDATED) void queryClient.invalidateQueries({ queryKey: key });
    }
  }, [pulse]);

  return (
    <StreamContext.Provider value={{ pulse, connected, rev: pulse?.rev ?? 0 }}>
      {children}
    </StreamContext.Provider>
  );
}

export function usePulse(): StreamValue {
  return useContext(StreamContext);
}
