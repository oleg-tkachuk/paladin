// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  DOT_TOKEN,
  FAVICON_SIZE_PX,
  ORIGINAL_HREF_ATTR,
  applyFavicon,
  dotColor,
  drawBadge,
  originalIconHref,
} from "./favicon";

const OWN = "/favicon.ico?v1";
const BADGED = "data:image/png;base64,AAAA";

function icons(...hrefs: string[]) {
  document.head.innerHTML = hrefs
    .map((h) => `<link rel="icon" href="${h}">`)
    .join("");
  return [...document.querySelectorAll("link")];
}

afterEach(() => {
  document.head.innerHTML = "";
  document.documentElement.removeAttribute("style");
});

describe("applyFavicon", () => {
  it("points every icon link at the badge, keeping each one's own", () => {
    const links = icons(OWN, "/icon.png");
    applyFavicon(BADGED);
    expect(links.map((l) => l.getAttribute("href"))).toEqual([BADGED, BADGED]);
    expect(links.map((l) => l.getAttribute(ORIGINAL_HREF_ATTR))).toEqual([
      OWN,
      "/icon.png",
    ]);
  });

  it("keeps the first own href across rewrites, and restores it", () => {
    const [link] = icons(OWN);
    applyFavicon(BADGED);
    applyFavicon("data:image/png;base64,BBBB");
    expect(originalIconHref()).toBe(OWN);
    applyFavicon(null);
    expect(link.getAttribute("href")).toBe(OWN);
    expect(link.hasAttribute(ORIGINAL_HREF_ATTR)).toBe(false);
  });

  it("leaves an icon it never rewrote alone on restore", () => {
    const [link] = icons(OWN);
    applyFavicon(null);
    expect(link.getAttribute("href")).toBe(OWN);
  });

  it("finds no own icon on a page that declares none", () => {
    expect(originalIconHref()).toBeNull();
  });
});

describe("dotColor", () => {
  it("reads each signal's colour from its theme token", () => {
    document.documentElement.style.setProperty(DOT_TOKEN.failed, " #ca2a3b ");
    document.documentElement.style.setProperty(DOT_TOKEN.running, "#b74616");
    expect(dotColor("failed")).toBe("#ca2a3b");
    expect(dotColor("running")).toBe("#b74616");
  });
});

describe("drawBadge", () => {
  it("draws the icon, cuts a ring, then fills the dot in the corner", () => {
    const ops: string[] = [];
    const ctx = {
      globalCompositeOperation: "source-over",
      fillStyle: "",
      clearRect: vi.fn(() => ops.push("clear")),
      drawImage: vi.fn(() => ops.push("icon")),
      beginPath: vi.fn(),
      arc: vi.fn((x: number, y: number, r: number) =>
        ops.push(`arc ${x} ${y} ${r}`),
      ),
      fill: vi.fn(function (this: {
        globalCompositeOperation: string;
        fillStyle: string;
      }) {
        ops.push(`fill ${ctx.globalCompositeOperation} ${ctx.fillStyle}`);
      }),
    };
    const icon = {} as CanvasImageSource;
    drawBadge(ctx as never, icon, "#ca2a3b", 100);
    expect(ctx.drawImage).toHaveBeenCalledWith(icon, 0, 0, 100, 100);
    expect(ops).toEqual([
      "clear",
      "icon",
      "arc 78 78 28",
      "fill destination-out ",
      "arc 78 78 22",
      "fill source-over #ca2a3b",
    ]);
  });

  it("draws at the favicon's size by default", () => {
    const ctx = {
      clearRect: vi.fn(),
      drawImage: vi.fn(),
      beginPath: vi.fn(),
      arc: vi.fn(),
      fill: vi.fn(),
    };
    drawBadge(ctx as never, {} as CanvasImageSource, "red");
    expect(ctx.clearRect).toHaveBeenCalledWith(
      0,
      0,
      FAVICON_SIZE_PX,
      FAVICON_SIZE_PX,
    );
  });
});
