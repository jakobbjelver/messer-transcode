// The wire shapes. Every field name here is a JSON tag in the Go types:
// internal/store/{dashboard,progress}.go, internal/report/report.go,
// internal/health/health.go and internal/server/api.go. Change one, change both.

export type VersionInfo = { version: string; commit: string; build_date: string };

export type WindowState = { spec: string; open: boolean; next_at?: string };

export type Dashboard = {
  total_files: number;
  total_bytes: number;
  reclaimed_bytes: number;
  done_bytes: number;
  projected_bytes: number;
  states: Record<string, number>;
  decisions: Record<string, number>;
  failures: number;
};

export type Live = {
  file_id: number;
  path: string;
  stage: string;
  fraction: number;
  speed: number;
  target_kbps: number;
  bytes: number;
  since: number;
};

export type StatusResponse = {
  /** Newest event id: the cache revision the stream also carries. */
  rev: number;
  version: VersionInfo;
  uptime_seconds: number;
  window: WindowState;
  dashboard: Dashboard;
  live: Live | null;
};

export type Schedule = {
  weekday: string;
  weekend: string;
  drain_before_minutes: number;
  timezone: string;
};

// report.Window has no JSON tags, so it marshals with its Go field names while
// Run's own fields are snake_case. Kept verbatim rather than renamed: the CLI's
// --json output is the same object.
export type Run = {
  /** The ledger id: the window's unix start, and the detail page's key. */
  id: number;
  Start: string;
  End: string;
  Spec: string;
  Kind: string;
  Open: boolean;
  done: number;
  failed: number;
  skipped: number;
  remuxed: number;
  encoded: number;
  bytes_before: number;
  bytes_after: number;
  busy_seconds: number;
  busy_percent: number;
  vmaf_mean?: number;
  vmaf_min?: number;
  vmaf_n?: number;
  failed_codes?: Record<string, number>;
  skipped_codes?: Record<string, number>;
};

export type RunsResponse = { schedule: Schedule; runs: Run[] };

/** One run with the rows it handled, grouped by outcome. */
export type RunResponse = {
  run: Run;
  placed: FileRow[];
  failed: FileRow[];
  skipped: FileRow[];
  open: FileRow[];
};

export type FileRow = {
  id: number;
  path: string;
  library: string;
  decision: string;
  reason_code: string;
  reason: string;
  state: string;
  video_codec: string;
  width: number;
  height: number;
  bitrate_kbps: number;
  target_kbps: number;
  size_bytes: number;
  /** -100 last · 0 normal · +100 next: the operator's ordering. */
  priority: number;
  /** Set only on a finished row; the Done view orders and shows by it. */
  finished_at?: number;
  bytes_before?: number;
  bytes_after?: number;
  vmaf?: number;
  attempts: number;
  /** The encoder's failure code (E_*), for a row that failed. */
  last_error_code?: string;
};

export type FileDetail = FileRow & {
  duration_s: number;
  bytes_before: number;
  bytes_after: number;
  vmaf: number;
  attempts: number;
  last_error: string;
  last_error_at: number;
  audio_tracks: string;
  sub_tracks: string;
};

export type FileResponse = {
  file: FileDetail;
  /** The VMAF this file's ladder tier is expected to deliver. */
  expected_vmaf: number;
  /** The ladder class the source's width selects (canon: height is not a resolution). */
  nominal_height: number;
  /** store.FileDetail.Reclaim: measured when done, projected when planned. */
  reclaim_bytes: number;
  /** store.FileDetail.AudioList: the stored track JSON as one sentence. */
  audio_list: string;
  /** store.FileDetail.SubList: the same for subtitles, keeping forced/SDH flags. */
  sub_list: string;
};

export type FilesResponse = {
  files: FileRow[];
  limit: number;
  offset: number;
  count: number;
  /** Rows matching the filter, not just this page — what a pager needs. */
  total: number;
};

export type EventRow = {
  at: number;
  level: string;
  code: string;
  message: string;
  file_id?: number;
};

export type HealthCheck = {
  name: string;
  status: "ok" | "degraded" | "broken";
  code?: string;
  detail?: string;
  fix?: string;
};

export type HealthReport = {
  status: "ok" | "degraded" | "broken";
  role: string;
  version: Record<string, string>;
  checks: HealthCheck[];
  last_encode?: number;
  queued: number;
  now: number;
  duration_ms: number;
};

export type ConfigResponse = {
  version: VersionInfo;
  uptime_seconds: number;
  config_path: string;
  db_path: string;
  db_size_bytes: number;
  config: Record<string, unknown>;
};

export type StreamPulse = {
  at: number;
  /** Newest event id. When it rises, everything built from file rows is stale. */
  rev: number;
  window: WindowState;
  dashboard: Dashboard | null;
  live: Live | null;
};

/** The operator's hand on the queue: a pause, and a window forced open or shut. */
export type Control = {
  paused: boolean;
  paused_at?: number;
  paused_by?: string;
  override: "" | "open" | "close";
  override_until?: number;
  override_by?: string;
};

export type ControlResponse = {
  control: Control;
  window: WindowState;
  /** What a claim would be answered with right now, and why. */
  claiming: { allowed: boolean; reason: string };
  now: number;
};

/** What a queue action did: rows it applied to, rows it refused and why. */
export type ActResult = {
  applied: number;
  /** Always an array on the wire; the `?` keeps an older server from crashing the page. */
  refused?: { id: number; state?: string; reason: "state" | "missing" }[] | null;
};

/** The queue tab strip's counts. Scoped to states, unlike the dashboard's. */
export type QueueSummary = {
  queued: number;
  encode: number;
  remux: number;
  inflight: number;
  failed: number;
  done: number;
  skipped: number;
  everything: number;
};
