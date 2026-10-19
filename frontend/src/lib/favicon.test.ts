// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  CONSOLE_ICON_HREF,
  DOT_TOKEN,
  FAVICON_SIZE_PX,
  applyFavicon,
  dotColor,
  drawBadge,
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
  it("points every icon link at the badge", () => {
    const links = icons(OWN, "/icon.png");
    applyFavicon(BADGED);
    expect(links.map((l) => l.getAttribute("href"))).toEqual([BADGED, BADGED]);
  });

  it("puts the console's own icon back", () => {
    const links = icons(OWN, "/icon.png");
    applyFavicon(BADGED);
    applyFavicon(null);
    expect(links.map((l) => l.getAttribute("href"))).toEqual([
      CONSOLE_ICON_HREF,
      CONSOLE_ICON_HREF,
    ]);
  });

  it("touches nothing on a page that declares no icon", () => {
    applyFavicon(BADGED);
    expect(document.head.innerHTML).toBe("");
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
