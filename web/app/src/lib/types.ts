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

/** One block of permitted work: the days it starts on, and its clock range. */
export type ScheduleWindow = {
  days: string[];
  start: string;
  end: string;
};

export type Schedule = {
  /** The source of truth. */
  windows?: ScheduleWindow[];
  /** Legacy pre-windows form: accepted on the wire, migrated by the server. */
  weekday?: string;
  weekend?: string;
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

// ── configuration ─────────────────────────────────────────────────────────
//
// These mirror internal/config exactly, including the fields a document may
// omit: absent and null both mean "not configured", and the server's renderer
// only ever writes keys the schema accepts.

export type Library = { name: string; root: string; extensions: string[]; exclude?: string[] };

export type QualityRow = { codec: string; height_gte: number; kbps: number; expected_vmaf?: number };

export type Quality = {
  mode: string;
  vmaf_target: number;
  vmaf_tolerance: number;
  table: QualityRow[];
  probe_outliers_above_kbps: number;
  min_saving_pct?: number;
};

export type AudioTranscode = { from: string[]; to: string; kbps: number };

export type Audio = {
  keep_languages?: string[];
  keep_original_language: boolean;
  max_tracks: number;
  drop_commentary: boolean;
  transcode?: AudioTranscode[];
};

export type Subtitles = {
  keep_languages?: string[];
  drop_commentary: boolean;
  drop_closed_captions: boolean;
  keep_forced: boolean;
};

export type Condition = {
  height_lt?: number;
  height_gte?: number;
  codec?: string;
  bitrate_kbps_lt?: number;
  bitrate_kbps_gte?: number;
  audio_codec_in?: string[];
  audio_tracks_gt?: number;
  default?: boolean;
};

export type Rule = { when: Condition; do: string; code: string };

export type Safety = {
  min_free_gb: number;
  size_ratio_min: number;
  size_ratio_max: number;
  duration_tolerance_s: number;
  keep_quarantine_days: number;
  verify_sample_seconds: number;
  verify_vmaf: boolean;
  max_attempts: number;
  quarantine?: string;
};

export type Notify = { radarr?: string; sonarr?: string; on?: string[]; jellyfin_refresh: boolean };

export type Workers = { encode: number; probe: number; ingest_buffer: number };

export type Paths = { scratch: string; quarantine: string };

export type ServerSettings = { listen: string; token?: string; url?: string };

/** A whole config.json. */
export type ConfigDoc = {
  version: number;
  libraries: Library[];
  schedule: Schedule;
  workers: Workers;
  paths: Paths;
  quality: Quality;
  audio: Audio;
  subtitles: Subtitles;
  rules: Rule[];
  safety: Safety;
  notify: Notify;
  server: ServerSettings;
};

/** Where the file on the volume stands relative to the newest revision. */
export type ConfigState = {
  head_rev: number;
  head_sha: string;
  /** "" is clean; "edited" is a hand edit; "invalid" is one that will not load. */
  dirty: "" | "edited" | "absent" | "invalid";
  disk_sha: string;
  disk_error?: string;
  /** "live" or "next claim". */
  applies: string;
  running_sha: string;
};

export type Revision = {
  rev: number;
  sha: string;
  created_at: number;
  actor: string;
  message: string;
  parent_rev?: number;
};

export type Problem = { path: string; message: string };

export type ChangeKind = "add" | "remove" | "change" | "reorder";

export type Change = {
  path: string;
  kind: ChangeKind;
  label: string;
  group: string;
  before?: unknown;
  after?: unknown;
};

export type Diff = {
  summary: { added: number; removed: number; changed: number; reordered: number };
  groups: { key: string; label: string; count: number }[];
  changes: Change[];
  text: string;
};

/** One window as the two cron lines a person would write. */
export type CronLine = { open: string; close: string; window: string };

export type DryRun = {
  ok: boolean;
  /** The schedule the draft would run, projected for anything that speaks cron. */
  schedule?: { cron: CronLine[]; drains: number };
  problems: Problem[];
  head_rev: number;
  head_sha: string;
  sha: string;
  state: ConfigState;
  diff?: Diff;
};

export type ConfigResponse = {
  version: VersionInfo;
  uptime_seconds: number;
  config_path: string;
  db_path: string;
  db_size_bytes: number;
  config: ConfigDoc;
  state: ConfigState;
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
