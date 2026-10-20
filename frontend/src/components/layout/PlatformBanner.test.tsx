import { beforeEach, describe, expect, it, vi } from "vitest";

import { render, screen } from "@/test/utils";
import type { PlatformBanner as Banner } from "@/lib/banner";

const h = vi.hoisted(() => ({ banner: null as Banner | null }));
vi.mock("@/context/ShellContext", () => ({
  useShell: () => ({ banner: h.banner }),
}));
vi.mock("@/hooks/usePreference", () => ({
  usePreference: () => [[], vi.fn(), vi.fn()],
}));

import { PlatformBanner } from "./PlatformBanner";

const BANNER: Banner = {
  content: "Maintenance on Sunday",
  severity: "critical",
  permanent: false,
  position: "top",
};

function edges() {
  render(
    <>
      <div data-testid="top">
        <PlatformBanner edge="top" />
      </div>
      <div data-testid="bottom">
        <PlatformBanner edge="bottom" />
      </div>
    </>,
  );
  return {
    top: screen.getByTestId("top").textContent,
    bottom: screen.getByTestId("bottom").textContent,
  };
}

describe("PlatformBanner", () => {
  beforeEach(() => {
    h.banner = null;
  });

  it("shows nothing when the operator set no banner", () => {
    expect(edges()).toEqual({ top: "", bottom: "" });
  });

  it("stands where the operator put it", () => {
    h.banner = BANNER;
    expect(edges()).toEqual({
      top: "Critical: Maintenance on Sunday",
      bottom: "",
    });
  });

  it("stands at both edges when asked to", () => {
    h.banner = { ...BANNER, position: "both" };
    const { top, bottom } = edges();
    expect(top).toContain("Maintenance on Sunday");
    expect(bottom).toContain("Maintenance on Sunday");
  });
});
