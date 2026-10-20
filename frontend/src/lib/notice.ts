// A notice: a short message an operator pins to the whole console (the
// platform banner) or to one resource (its labels), after Argo CD's
// ui.banner and notice.argocd.argoproj.io/* annotations. Pure, so the
// server, the shell and the pages read a notice the same way.

/** How loud a notice is, quietest first. */
export const NOTICE_SEVERITIES = ["info", "warning", "critical"] as const;
export type NoticeSeverity = (typeof NOTICE_SEVERITIES)[number];

export interface Notice {
  /** What the reader should know, as the operator wrote it. */
  content: string;
  severity: NoticeSeverity;
  /** Where to read more: an http(s) address or a page of this console. */
  url?: string;
  /** Shown every time: the reader cannot dismiss it. */
  permanent: boolean;
}

/** The fields of a notice as text, before they are checked. */
export interface RawNotice {
  content?: string;
  severity?: string;
  url?: string;
  permanent?: string | boolean;
}

/** The text that marks a flag as on, in a label or an environment variable. */
const FLAG_ON = "true";

/** A notice whose content is longer is cut, so one label cannot fill a page. */
const MAX_CONTENT = 500;

const ALLOWED_PROTOCOLS = new Set(["https:", "http:"]);

/**
 * The severity a text names; anything else is info. A typo in a severity
 * should still show the notice, at its quietest, rather than drop it.
 */
function severityOf(raw: string | undefined): NoticeSeverity {
  const v = (raw ?? "").trim().toLowerCase();
  return NOTICE_SEVERITIES.find((s) => s === v) ?? "info";
}

/**
 * The address a notice may link to: an absolute http(s) URL, or a path in
 * this console. Anything else — `javascript:`, a protocol-relative `//host`
 * — is dropped, because the text comes from a label any tenant editor can
 * write and the link is shown to every reader.
 */
export function noticeUrl(raw: string | undefined): string | undefined {
  const v = (raw ?? "").trim();
  if (!v) return undefined;
  if (v.startsWith("/")) {
    return v.startsWith("//") || v.includes("\\") ? undefined : v;
  }
  try {
    return ALLOWED_PROTOCOLS.has(new URL(v).protocol) ? v : undefined;
  } catch {
    return undefined;
  }
}

/** A notice from its raw fields, or null when it has nothing to say. */
export function readNotice(raw: RawNotice): Notice | null {
  const content = (raw.content ?? "").trim().slice(0, MAX_CONTENT);
  if (!content) return null;
  const url = noticeUrl(raw.url);
  return {
    content,
    severity: severityOf(raw.severity),
    ...(url ? { url } : {}),
    permanent:
      raw.permanent === true ||
      (typeof raw.permanent === "string" &&
        raw.permanent.trim().toLowerCase() === FLAG_ON),
  };
}

/**
 * The label keys a resource carries its notice under. The prefix follows the
 * `paladin.io/` the charts label their own objects with.
 */
const NOTICE_LABELS = {
  content: "notice.paladin.io/content",
  severity: "notice.paladin.io/severity",
  url: "notice.paladin.io/url",
  permanent: "notice.paladin.io/permanent",
} as const;

/** Whether a label is one of a notice's, which a list of labels leaves out. */
export function isNoticeLabel(key: string): boolean {
  return Object.values<string>(NOTICE_LABELS).includes(key);
}

/** The notice a resource's labels carry, or null. */
export function noticeFromLabels(
  labels: Readonly<Record<string, string>> | undefined,
): Notice | null {
  if (!labels) return null;
  return readNotice({
    content: labels[NOTICE_LABELS.content],
    severity: labels[NOTICE_LABELS.severity],
    url: labels[NOTICE_LABELS.url],
    permanent: labels[NOTICE_LABELS.permanent],
  });
}

/**
 * The labels with the notice written into them, or taken out when it is
 * null. Every other label is kept as it was, and a default (info, not
 * permanent, no link) is left out rather than stored.
 */
export function withNoticeLabels(
  labels: Readonly<Record<string, string>>,
  notice: Notice | null,
): Record<string, string> {
  const next = { ...labels };
  for (const key of Object.values(NOTICE_LABELS)) delete next[key];
  if (!notice) return next;
  next[NOTICE_LABELS.content] = notice.content;
  if (notice.severity !== "info") {
    next[NOTICE_LABELS.severity] = notice.severity;
  }
  if (notice.url) next[NOTICE_LABELS.url] = notice.url;
  if (notice.permanent) next[NOTICE_LABELS.permanent] = FLAG_ON;
  return next;
}

/**
 * A short, stable name for what a notice says. A dismissal is remembered
 * under it, so the same notice stays dismissed and a changed one — new text,
 * a new link, a louder severity — shows again. FNV-1a: not a secret, only a
 * key, and short enough to keep a few dozen in the user's settings.
 */
export function noticeKey(notice: Notice): string {
  const text = [notice.severity, notice.url ?? "", notice.content].join("\n");
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(16).padStart(8, "0");
}

/** How many dismissals the settings keep; the oldest go first. */
const MAX_DISMISSED = 50;

/** The dismissed notices' keys, from what the settings hold. */
export function readDismissed(raw: unknown): string[] {
  return Array.isArray(raw)
    ? raw.filter((k): k is string => typeof k === "string")
    : [];
}

/** The stored keys with one more, newest last, at most MAX_DISMISSED. */
export function withDismissed(raw: unknown, key: string): string[] {
  const kept = readDismissed(raw).filter((k) => k !== key);
  return [...kept, key].slice(-MAX_DISMISSED);
}
