import { describe, expect, it } from "vitest";

import {
  formatBuildLabel,
  isBuildSkew,
  LOCAL_BUILD_VERSION,
  shortCommit,
  SHORT_COMMIT_LENGTH,
} from "./build-info";

const FULL_SHA = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678";

describe("formatBuildLabel", () => {
  it("puts the short commit in parentheses after a v-prefixed release", () => {
    expect(formatBuildLabel("11.9.0", FULL_SHA)).toBe("v11.9.0 (a1b2c3d)");
  });

  it("does not double a v the version already carries", () => {
    expect(formatBuildLabel("v11.9.0", "a1b2c3d")).toBe("v11.9.0 (a1b2c3d)");
  });

  it("keeps a pre-release suffix", () => {
    expect(formatBuildLabel("12.0.0-rc.1", "a1b2c3d")).toBe(
      "v12.0.0-rc.1 (a1b2c3d)",
    );
  });

  it("drops the brackets when the commit is unknown", () => {
    expect(formatBuildLabel("11.9.0", "")).toBe("v11.9.0");
  });

  it("calls an unversioned build local", () => {
    expect(formatBuildLabel("", "a1b2c3d")).toBe(
      `${LOCAL_BUILD_VERSION} (a1b2c3d)`,
    );
    expect(formatBuildLabel("  ", "")).toBe(LOCAL_BUILD_VERSION);
  });
});

describe("shortCommit", () => {
  it("abbreviates to git's default length", () => {
    expect(shortCommit(FULL_SHA)).toHaveLength(SHORT_COMMIT_LENGTH);
  });

  it("leaves a short or empty SHA as it is", () => {
    expect(shortCommit("abc")).toBe("abc");
    expect(shortCommit("")).toBe("");
  });
});

describe("isBuildSkew", () => {
  it("is a skew when both commits are known and differ", () => {
    expect(isBuildSkew(FULL_SHA, "0000000")).toBe(true);
  });

  it("compares abbreviated forms, so a full and a short SHA of one commit match", () => {
    expect(isBuildSkew(FULL_SHA, "a1b2c3d")).toBe(false);
  });

  it("is not a skew when either side is unknown", () => {
    expect(isBuildSkew("", "a1b2c3d")).toBe(false);
    expect(isBuildSkew(FULL_SHA, "")).toBe(false);
  });
});
