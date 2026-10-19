// The tab's icon with a dot for the user's operations, the same signal the
// tab's title spells out (lib/tabTitle). The dot is drawn over the console's
// own icon, so the tab still reads as Paladin.
import type { ActivitySignal } from "./tabTitle";

/** Edge of the square the badged icon is drawn on, in pixels. */
export const FAVICON_SIZE_PX = 32;
/** The dot's radius, as a share of the icon's edge. */
const DOT_RADIUS_SHARE = 0.22;
/** The ring cut around the dot so it reads on any icon, as a share too. */
const DOT_RING_SHARE = 0.06;

/** The theme token whose colour each signal's dot takes. */
export const DOT_TOKEN: Record<Exclude<ActivitySignal, "idle">, string> = {
  running: "--primary",
  failed: "--destructive",
};

/** Marks the icon links the console has rewritten, holding their own href. */
export const ORIGINAL_HREF_ATTR = "data-original-href";

const ICON_LINKS = 'link[rel~="icon"]';

type Ctx = Pick<
  CanvasRenderingContext2D,
  | "clearRect"
  | "drawImage"
  | "beginPath"
  | "arc"
  | "fill"
  | "globalCompositeOperation"
  | "fillStyle"
>;

/** Draws the icon with the dot in its lower right corner. */
export function drawBadge(
  ctx: Ctx,
  icon: CanvasImageSource,
  color: string,
  size: number = FAVICON_SIZE_PX,
): void {
  const r = size * DOT_RADIUS_SHARE;
  const c = size - r;
  ctx.clearRect(0, 0, size, size);
  ctx.drawImage(icon, 0, 0, size, size);
  // Clear a ring first, so the dot does not run into the icon under it.
  ctx.globalCompositeOperation = "destination-out";
  ctx.beginPath();
  ctx.arc(c, c, r + size * DOT_RING_SHARE, 0, 2 * Math.PI);
  ctx.fill();
  ctx.globalCompositeOperation = "source-over";
  ctx.fillStyle = color;
  ctx.beginPath();
  ctx.arc(c, c, r, 0, 2 * Math.PI);
  ctx.fill();
}

/** The colour of a signal's dot in the current theme. */
export function dotColor(
  signal: Exclude<ActivitySignal, "idle">,
  root: Element = document.documentElement,
): string {
  return getComputedStyle(root).getPropertyValue(DOT_TOKEN[signal]).trim();
}

/** The console's own icon, as the page declares it before any rewrite. */
export function originalIconHref(doc: Document = document): string | null {
  const link = doc.querySelector<HTMLLinkElement>(ICON_LINKS);
  if (!link) return null;
  return link.getAttribute(ORIGINAL_HREF_ATTR) ?? link.getAttribute("href");
}

/**
 * Points every icon link at `href`, or back at its own icon when `href` is
 * null. Each link keeps its own href aside the first time it is rewritten.
 */
export function applyFavicon(href: string | null, doc: Document = document) {
  doc.querySelectorAll<HTMLLinkElement>(ICON_LINKS).forEach((link) => {
    const own = link.getAttribute(ORIGINAL_HREF_ATTR);
    if (href === null) {
      if (own !== null) {
        link.setAttribute("href", own);
        link.removeAttribute(ORIGINAL_HREF_ATTR);
      }
      return;
    }
    if (own === null) {
      link.setAttribute(ORIGINAL_HREF_ATTR, link.getAttribute("href") ?? "");
    }
    link.setAttribute("href", href);
  });
}

/** The icon at `src` with a dot of `color`, as a data URL. */
export async function badgedIconUrl(
  src: string,
  color: string,
): Promise<string | null> {
  const icon = new Image();
  icon.src = src;
  await icon.decode();
  const canvas = document.createElement("canvas");
  canvas.width = FAVICON_SIZE_PX;
  canvas.height = FAVICON_SIZE_PX;
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  drawBadge(ctx, icon, color);
  return canvas.toDataURL();
}
