import type { Change, ConfigDoc, Diff } from "./types";

// ── addressing ────────────────────────────────────────────────────────────
//
// The server addresses a change by JSON pointer ("/quality/table/0/kbps"), and
// so does this app. One addressing scheme shared by the diff, the forms and the
// history means a change in the dialog and a field on screen are the same fact,
// not two renderings of it that can drift.

function segments(path: string): string[] {
  return path.split("/").filter((s) => s !== "");
}

/** Read a value out of the document by pointer. */
export function getPath(doc: unknown, path: string): unknown {
  let cur: unknown = doc;
  for (const key of segments(path)) {
    if (cur === null || cur === undefined) return undefined;
    if (Array.isArray(cur)) {
      const i = Number(key);
      cur = Number.isInteger(i) ? cur[i] : undefined;
      continue;
    }
    if (typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[key];
  }
  return cur;
}

/**
 * Return a copy of the document with one leaf replaced.
 *
 * Copy-on-write down the path only: the untouched branches keep their identity,
 * so React's memo checks still mean something and a 300-row ladder is not
 * cloned on every keystroke.
 */
export function setPath<T>(doc: T, path: string, value: unknown): T {
  const keys = segments(path);
  if (keys.length === 0) return doc;
  const head = cloneContainer(doc);
  let cur: any = head;
  for (let i = 0; i < keys.length - 1; i++) {
    const key = keys[i];
    const nextKey = keys[i + 1];
    const existing = cur[key];
    const child = Array.isArray(existing) ? [...existing] : { ...(existing ?? {}) };
    if (existing === undefined) {
      // A missing branch is created as the type the next key implies.
      cur[key] = /^\d+$/.test(nextKey) ? [] : {};
    }
    cur[key] = child;
    cur = child;
  }
  const last = keys[keys.length - 1];
  if (Array.isArray(cur) && /^\d+$/.test(last)) {
    cur[Number(last)] = value;
  } else {
    cur[last] = value;
  }
  return head;
}

/** Return a copy with one leaf removed (for keys the schema treats as absent). */
export function deletePath<T>(doc: T, path: string): T {
  const keys = segments(path);
  if (keys.length === 0) return doc;
  const head = cloneContainer(doc);
  let cur: any = head;
  for (let i = 0; i < keys.length - 1; i++) {
    const next = cur[keys[i]];
    if (next === undefined || next === null) return head;
    cur[keys[i]] = Array.isArray(next) ? [...next] : { ...next };
    cur = cur[keys[i]];
  }
  const last = keys[keys.length - 1];
  if (Array.isArray(cur) && /^\d+$/.test(last)) cur.splice(Number(last), 1);
  else delete cur[last];
  return head;
}

function cloneContainer<T>(v: T): T {
  if (Array.isArray(v)) return [...v] as unknown as T;
  if (v && typeof v === "object") return { ...(v as object) } as T;
  return v;
}

// ── values as a person reads them ─────────────────────────────────────────

/** One value, rendered for a diff row. Never "undefined", never "[object]". */
export function showValue(v: unknown): string {
  if (v === undefined || v === null) return "not set";
  if (typeof v === "boolean") return v ? "on" : "off";
  if (typeof v === "number") return String(v);
  if (typeof v === "string") return v === "" ? "(empty)" : v;
  if (Array.isArray(v)) return v.length === 0 ? "(none)" : v.map(showValue).join(", ");
  if (typeof v === "object") {
    // A window or a rule reads as itself, not as its JSON.
    const o = v as Record<string, unknown>;
    if (Array.isArray(o.days) && typeof o.start === "string" && typeof o.end === "string") {
      return `${(o.days as string[]).join(", ")}, ${o.start}–${o.end}`;
    }
    if (typeof o.code === "string" && o.code) return `rule ${o.code}`;
    return JSON.stringify(v);
  }
  return String(v);
}

/** A comma list as an editable field value, and back. */
export function listText(v?: string[]): string {
  return (v ?? []).join(", ");
}

export function parseList(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}

/** "2 changes · 1 addition" — the one line above the detail. */
export function summaryLine(d: Diff): string {
  const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
  const parts: string[] = [];
  if (d.summary.changed) parts.push(count(d.summary.changed, "change", "changes"));
  if (d.summary.added) parts.push(count(d.summary.added, "addition", "additions"));
  if (d.summary.removed) parts.push(count(d.summary.removed, "removal", "removals"));
  if (d.summary.reordered) parts.push(count(d.summary.reordered, "reorder", "reorders"));
  if (parts.length === 0) return "nothing changes";
  return parts.join(" · ");
}

/** Group changes by section, in the order the groups arrived (sorted by key). */
export function byGroup(changes: Change[]): { key: string; label: string; changes: Change[] }[] {
  const order: string[] = [];
  const map = new Map<string, { key: string; label: string; changes: Change[] }>();
  for (const c of changes) {
    let g = map.get(c.group);
    if (!g) {
      g = { key: c.group, label: groupTitle(c.group), changes: [] };
      map.set(c.group, g);
      order.push(c.group);
    }
    g.changes.push(c);
  }
  return order.map((k) => map.get(k)!);
}

function groupTitle(key: string): string {
  return key ? key.charAt(0).toUpperCase() + key.slice(1) : "Document";
}

/**
 * The fields the forms steer. Everything else is editable, but only in the
 * document tab: a rules editor is a different release, and pretending otherwise
 * with three inputs would be worse than saying "edit the document".
 */
export const FORM_SECTIONS = [
  "schedule",
  "workers",
  "paths",
  "quality",
  "audio",
  "subtitles",
  "safety",
] as const;

export type FormSection = (typeof FORM_SECTIONS)[number];

/** Sections shown on the form tab as a summary with a pointer to the document. */
export const DOCUMENT_ONLY: { key: keyof ConfigDoc; label: string; reason: string }[] = [
  { key: "rules", label: "Rules", reason: "an ordered decision list: order is the meaning" },
  { key: "libraries", label: "Libraries", reason: "a path the node mounts, not a tunable" },
  { key: "notify", label: "Notify", reason: "URLs and a webhook target" },
  { key: "server", label: "Server", reason: "the token lives in a secret file, not here" },
];
