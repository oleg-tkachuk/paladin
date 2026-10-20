// The platform banner: one message the operator puts across the whole
// console — a maintenance window, a migration, a policy change — after Argo
// CD's ui.bannercontent. It is the deployment's to say, so it comes from the
// BFF's environment and is read on every request rather than baked into the
// image; /api/shell hands it to the console with the rest of the chrome.
import { readNotice, type Notice } from "@/lib/notice";

/** The environment variables the banner is set through (the chart's `banner`). */
export const BANNER_ENV = {
  content: "PALADIN_BANNER_CONTENT",
  severity: "PALADIN_BANNER_SEVERITY",
  url: "PALADIN_BANNER_URL",
  permanent: "PALADIN_BANNER_PERMANENT",
  position: "PALADIN_BANNER_POSITION",
} as const;

/** Where the banner stands: above the page, below it, or both. */
const BANNER_POSITIONS = ["top", "bottom", "both"] as const;
type BannerPosition = (typeof BANNER_POSITIONS)[number];

export interface PlatformBanner extends Notice {
  position: BannerPosition;
}

type Fields = { [K in keyof typeof BANNER_ENV]?: unknown };

const text = (v: unknown): string | undefined =>
  typeof v === "string" ? v : undefined;

/**
 * A banner from its fields, or null when there is no content. An unknown
 * position is the top, where a banner is read first.
 */
function bannerOf(fields: Fields): PlatformBanner | null {
  const notice = readNotice({
    content: text(fields.content),
    severity: text(fields.severity),
    url: text(fields.url),
    permanent:
      typeof fields.permanent === "boolean"
        ? fields.permanent
        : text(fields.permanent),
  });
  if (!notice) return null;
  const raw = (text(fields.position) ?? "").trim().toLowerCase();
  const position = BANNER_POSITIONS.find((p) => p === raw) ?? "top";
  return { ...notice, position };
}

/** The banner the environment sets, or null. */
export function bannerFromEnv(
  env: Readonly<Record<string, string | undefined>>,
): PlatformBanner | null {
  return bannerOf({
    content: env[BANNER_ENV.content],
    severity: env[BANNER_ENV.severity],
    url: env[BANNER_ENV.url],
    permanent: env[BANNER_ENV.permanent],
    position: env[BANNER_ENV.position],
  });
}

/**
 * The banner /api/shell sent, checked again in the browser: the section is
 * JSON, and a field the console does not expect should cost the banner, not
 * the shell.
 */
export function bannerFromJson(json: unknown): PlatformBanner | null {
  return json && typeof json === "object" ? bannerOf(json as Fields) : null;
}

/** Whether the banner stands at this edge of the page. */
export function bannerAt(
  banner: PlatformBanner,
  edge: "top" | "bottom",
): boolean {
  return banner.position === "both" || banner.position === edge;
}
