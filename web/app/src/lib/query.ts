import { QueryClient } from "@tanstack/react-query";

/**
 * One client for the whole app.
 *
 * `refetchOnWindowFocus` is off on purpose: the SSE stream is the source of
 * truth, and a focus refetch would be a second one. The stale times are floors
 * for when the stream is down, not the normal refresh path — a screen refetches
 * because `rev` moved, not because time passed.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      gcTime: 5 * 60_000,
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});
