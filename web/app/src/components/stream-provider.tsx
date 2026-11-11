import { createContext, useContext } from "react";

import { useStream } from "@/hooks/use-stream";
import type { StreamPulse } from "@/lib/types";

type StreamValue = { pulse: StreamPulse | null; connected: boolean };

const StreamContext = createContext<StreamValue>({ pulse: null, connected: false });

/**
 * One EventSource for the whole app. The shell's status badge and the overview
 * both read it; two connections would be two truths and two reconnects.
 *
 * The `/ui` sink mounts this provider but opens no stream: the sink measures
 * static style, and an open SSE connection never lets a harness see the page
 * idle — it fails the run with a navigation timeout rather than a style finding.
 */
export function StreamProvider({ children }: { children: React.ReactNode }) {
  const enabled = typeof window === "undefined" || window.location.pathname !== "/ui";
  const value = useStream(enabled);
  return <StreamContext.Provider value={value}>{children}</StreamContext.Provider>;
}

export function usePulse(): StreamValue {
  return useContext(StreamContext);
}
