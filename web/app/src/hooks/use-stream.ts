import { useEffect, useState } from "react";

import { STREAM_URL } from "@/lib/api";
import type { StreamPulse } from "@/lib/types";

/**
 * The dashboard's live channel: one SSE connection, typed payloads, no polling.
 * A dropped stream is the browser's to retry; `connected` is here so a screen
 * can say so instead of showing a figure that silently stopped moving.
 *
 * `enabled: false` opens nothing. The conformance sink uses it: an open stream
 * means the page never goes idle, and a style surface is not a live one.
 */
export function useStream(enabled = true) {
  const [pulse, setPulse] = useState<StreamPulse | null>(null);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!enabled) return;
    if (typeof EventSource === "undefined") return;
    const es = new EventSource(STREAM_URL);
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    es.onmessage = (m) => {
      try {
        const next = JSON.parse(m.data) as StreamPulse;
        setPulse(next);
        setConnected(true);
      } catch {
        // A truncated frame is not worth a state update; the next one lands.
      }
    };
    return () => es.close();
  }, [enabled]);

  return { pulse, connected };
}
