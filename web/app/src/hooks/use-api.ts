import { useCallback, useEffect, useRef, useState } from "react";

import { ApiError } from "@/lib/api";

type State<T> = { data: T | null; error: ApiError | null; loading: boolean };

/**
 * One GET, re-run when `key` changes. Deliberately not a query library: five
 * screens and a live stream do not need a cache, and a stale cache here would
 * report work that already finished.
 */
export function useApi<T>(key: string, fetcher: () => Promise<T>) {
  const [state, setState] = useState<State<T>>({ data: null, error: null, loading: true });
  const [nonce, setNonce] = useState(0);
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    let alive = true;
    setState((s) => ({ ...s, loading: true }));
    fetcherRef
      .current()
      .then((data) => {
        if (alive) setState({ data, error: null, loading: false });
      })
      .catch((err: unknown) => {
        if (!alive) return;
        const error =
          err instanceof ApiError
            ? err
            : new ApiError("E_NETWORK", err instanceof Error ? err.message : String(err), 0);
        setState((s) => ({ data: s.data, error, loading: false }));
      });
    return () => {
      alive = false;
    };
  }, [key, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { ...state, reload };
}
