// The platform banner: one message the operator puts across the whole
// console — a maintenance window, a migration, a policy change — after Argo
// CD's ui.bannercontent. It is the deployment's to say, so it comes from the
// console server's environment (server/cmd/paladin-console) rather than the
// image; /api/banner hands it to the console's chrome as written.
import { readNotice, type Notice } from "@/lib/notice";

/** Where the banner stands: above the page, below it, or both. */
const BANNER_POSITIONS = ["top", "bottom", "both"] as const;
type BannerPosition = (typeof BANNER_POSITIONS)[number];

export interface PlatformBanner extends Notice {
  position: BannerPosition;
}

/** The banner's fields as the console's server sends them, unchecked. */
type Fields = {
  content?: unknown;
  severity?: unknown;
  url?: unknown;
  permanent?: unknown;
  position?: unknown;
};

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

/**
 * The banner /api/banner sent, checked here in the browser: the answer is
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
