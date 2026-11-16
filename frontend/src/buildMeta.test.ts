import { describe, expect, it } from "vitest";

import { UNSET_META, buildMeta, resolveMeta } from "./buildMeta";

const NOW = new Date("2026-11-16T12:00:00.000Z");

describe("resolveMeta", () => {
  it.each(["undefined", "none", "unknown", "", "   "])(
    "skips %j as unset",
    (placeholder) => {
      expect(resolveMeta([placeholder], UNSET_META)).toBe(UNSET_META);
    },
  );

  it("takes the first value that is set, trimmed", () => {
    expect(resolveMeta([undefined, " 2.0.0 ", "3.0.0"], UNSET_META)).toBe(
      "2.0.0",
    );
    expect(resolveMeta(["1.0.0", "2.0.0"], UNSET_META)).toBe("1.0.0");
  });

  it("falls back when nothing is set", () => {
    expect(resolveMeta([undefined, "unknown"], "fallback")).toBe("fallback");
  });
});

describe("buildMeta", () => {
  it("reads the build args deploy/Dockerfile passes", () => {
    expect(
      buildMeta(
        { APP_VERSION: "1.4.0", GIT_COMMIT_HASH: "abc1234", BUILD_TIME: "t" },
        "9.9.9",
        NOW,
      ),
    ).toEqual({ version: "1.4.0", gitSha: "abc1234", buildTime: "t" });
  });

  it("takes the version from the VERSION file when the build arg is a placeholder", () => {
    expect(
      buildMeta({ APP_VERSION: "undefined" }, "2.1.0\n", NOW).version,
    ).toBe("2.1.0");
  });

  it("reports no version and no commit, and the time it ran, when given nothing", () => {
    expect(
      buildMeta(
        {
          APP_VERSION: "undefined",
          GIT_COMMIT_HASH: "undefined",
          BUILD_TIME: "undefined",
        },
        undefined,
        NOW,
      ),
    ).toEqual({
      version: UNSET_META,
      gitSha: UNSET_META,
      buildTime: NOW.toISOString(),
    });
  });
});
