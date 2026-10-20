import { describe, expect, it } from "vitest";

import { BANNER_ENV, bannerAt, bannerFromEnv, bannerFromJson } from "./banner";

describe("bannerFromEnv", () => {
  it("is null when no content is set", () => {
    expect(bannerFromEnv({})).toBeNull();
    expect(
      bannerFromEnv({
        [BANNER_ENV.content]: "",
        [BANNER_ENV.severity]: "critical",
      }),
    ).toBeNull();
  });

  it("defaults to an info banner at the top that can be dismissed", () => {
    expect(
      bannerFromEnv({ [BANNER_ENV.content]: "Maintenance on Sunday" }),
    ).toEqual({
      content: "Maintenance on Sunday",
      severity: "info",
      permanent: false,
      position: "top",
    });
  });

  it("reads every variable", () => {
    expect(
      bannerFromEnv({
        PALADIN_BANNER_CONTENT: "Read-only until 18:00 UTC",
        PALADIN_BANNER_SEVERITY: "critical",
        PALADIN_BANNER_URL: "https://status.example.com",
        PALADIN_BANNER_PERMANENT: "true",
        PALADIN_BANNER_POSITION: "Bottom",
      }),
    ).toEqual({
      content: "Read-only until 18:00 UTC",
      severity: "critical",
      url: "https://status.example.com",
      permanent: true,
      position: "bottom",
    });
  });

  it("puts a banner with an unknown position at the top", () => {
    expect(
      bannerFromEnv({
        [BANNER_ENV.content]: "x",
        [BANNER_ENV.position]: "sidebar",
      })?.position,
    ).toBe("top");
  });
});

describe("bannerFromJson", () => {
  it("reads what the shell sent", () => {
    expect(
      bannerFromJson({
        content: "x",
        severity: "warning",
        permanent: true,
        position: "both",
      }),
    ).toEqual({
      content: "x",
      severity: "warning",
      permanent: true,
      position: "both",
    });
  });

  it("is null for anything else", () => {
    expect(bannerFromJson(null)).toBeNull();
    expect(bannerFromJson("x")).toBeNull();
    expect(bannerFromJson({ content: 7 })).toBeNull();
  });
});

describe("bannerAt", () => {
  const banner = bannerFromEnv({ [BANNER_ENV.content]: "x" })!;

  it("stands where it is put", () => {
    expect(bannerAt(banner, "top")).toBe(true);
    expect(bannerAt(banner, "bottom")).toBe(false);
    expect(bannerAt({ ...banner, position: "bottom" }, "top")).toBe(false);
    expect(bannerAt({ ...banner, position: "both" }, "top")).toBe(true);
    expect(bannerAt({ ...banner, position: "both" }, "bottom")).toBe(true);
  });
});
