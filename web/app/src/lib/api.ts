import type {
  ActResult,
  ConfigResponse,
  ControlResponse,
  EventRow,
  FileResponse,
  FilesResponse,
  HealthReport,
  Live,
  QueueSummary,
  RunsResponse,
  StatusResponse,
} from "./types";

/** The server's error envelope: {"error":{"code":"E_STORE","message":"…"}}. */
export class ApiError extends Error {
  code: string;
  status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

/** Normalise whatever `useQuery` threw into the server's error envelope. */
export function asApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  if (err instanceof Error) return new ApiError("E_NETWORK", err.message, 0);
  return new ApiError("E_NETWORK", String(err), 0);
}

async function raise(res: Response): Promise<never> {
  let code = "E_HTTP_" + res.status;
  let message = res.statusText;
  try {
    const body = (await res.json()) as { error?: { code?: string; message?: string } };
    code = body.error?.code ?? code;
    message = body.error?.message ?? message;
  } catch {
    // A non-JSON body (a proxy page, a dropped connection) keeps the status text.
  }
  throw new ApiError(code, message, res.status);
}

async function get<T>(path: string, okStatuses: number[] = []): Promise<T> {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  // A 503 that still carries a body is a verdict, not a failure: /api/v1/health
  // answers 503 when the pipeline is broken *and* sends the report that says why.
  if (!res.ok && !okStatuses.includes(res.status)) await raise(res);
  return (await res.json()) as T;
}

/**
 * Every mutation goes through here, which is also the one place the JSON
 * content type is set — the server refuses a mutation without it (a cross-site
 * form post cannot set it without a preflight).
 */
async function send<T>(method: "POST", path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  if (!res.ok) await raise(res);
  return (await res.json()) as T;
}

export type FileQuery = {
  state?: string;
  /** Comma list, for the "in flight" tab: three states, one filter. */
  state_in?: string;
  decision?: string;
  /** Substring of the path. */
  q?: string;
  sort?: "queue" | "recent" | "size";
  limit?: number;
  offset?: number;
};

function fileQuery(q: FileQuery): string {
  const p = new URLSearchParams();
  if (q.state) p.set("state", q.state);
  if (q.state_in) p.set("state_in", q.state_in);
  if (q.decision) p.set("decision", q.decision);
  if (q.q) p.set("q", q.q);
  if (q.sort) p.set("sort", q.sort);
  if (q.limit) p.set("limit", String(q.limit));
  if (q.offset) p.set("offset", String(q.offset));
  const s = p.toString();
  return s ? "?" + s : "";
}

export type QueueAction = "retry" | "skip" | "priority";

export const api = {
  status: () => get<StatusResponse>("/api/v1/status"),
  runs: (last: number) => get<RunsResponse>(`/api/v1/runs?last=${last}`),
  files: (q: FileQuery = {}) => get<FilesResponse>("/api/v1/files" + fileQuery(q)),
  file: (id: number) => get<FileResponse>(`/api/v1/files/${id}`),
  events: (limit: number) => get<{ events: EventRow[] }>(`/api/v1/events?limit=${limit}`),
  live: () => get<{ live: Live | null }>("/api/v1/live"),
  config: () => get<ConfigResponse>("/api/v1/config"),
  health: () => get<HealthReport>("/api/v1/health", [503]),

  control: () => get<ControlResponse>("/api/v1/control"),
  queue: () => get<{ counts: QueueSummary }>("/api/v1/queue"),
  pause: (paused: boolean) => send<ControlResponse>("POST", "/api/v1/control/pause", { paused }),
  window: (action: "open" | "close" | "auto", minutes = 0) =>
    send<ControlResponse>("POST", "/api/v1/control/window", { action, minutes }),
  act: (action: QueueAction, ids: number[], priority = 0) =>
    send<ActResult>("POST", "/api/v1/queue/act", { action, ids, priority }),
};

export const STREAM_URL = "/api/v1/stream";
