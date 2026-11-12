import { afterEach, describe, expect, it, vi } from "vitest";

import { BANNER_ENV } from "@/lib/banner";

import { GET } from "./route";

afterEach(() => vi.unstubAllEnvs());

describe("GET /api/banner", () => {
  it("answers the banner the environment configures, uncached", async () => {
    vi.stubEnv(BANNER_ENV.content, "Maintenance at 18:00");
    vi.stubEnv(BANNER_ENV.severity, "warning");
    const res = GET();
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(await res.json()).toMatchObject({
      content: "Maintenance at 18:00",
      severity: "warning",
    });
  });

  it("answers no banner when none is configured", async () => {
    vi.stubEnv(BANNER_ENV.content, "");
    expect(await GET().json()).toBeNull();
  });
});
