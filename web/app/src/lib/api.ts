import type {
  ConfigResponse,
  EventRow,
  FileResponse,
  FilesResponse,
  HealthReport,
  Live,
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

async function get<T>(path: string, okStatuses: number[] = []): Promise<T> {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  // A 503 that still carries a body is a verdict, not a failure: /api/v1/health
  // answers 503 when the pipeline is broken *and* sends the report that says why.
  if (!res.ok && !okStatuses.includes(res.status)) {
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
  return (await res.json()) as T;
}

type FileQuery = { state?: string; decision?: string; limit?: number; offset?: number };

function fileQuery(q: FileQuery): string {
  const p = new URLSearchParams();
  if (q.state) p.set("state", q.state);
  if (q.decision) p.set("decision", q.decision);
  if (q.limit) p.set("limit", String(q.limit));
  if (q.offset) p.set("offset", String(q.offset));
  const s = p.toString();
  return s ? "?" + s : "";
}

export const api = {
  status: () => get<StatusResponse>("/api/v1/status"),
  runs: (last: number) => get<RunsResponse>(`/api/v1/runs?last=${last}`),
  files: (q: FileQuery = {}) => get<FilesResponse>("/api/v1/files" + fileQuery(q)),
  file: (id: number) => get<FileResponse>(`/api/v1/files/${id}`),
  events: (limit: number) => get<{ events: EventRow[] }>(`/api/v1/events?limit=${limit}`),
  live: () => get<{ live: Live | null }>("/api/v1/live"),
  config: () => get<ConfigResponse>("/api/v1/config"),
  health: () => get<HealthReport>("/api/v1/health", [503]),
};

export const STREAM_URL = "/api/v1/stream";
