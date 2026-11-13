import { describe, expect, it } from "vitest";

import { bannerAt, bannerFromJson } from "./banner";

describe("bannerFromJson", () => {
  it("is null when no content is set", () => {
    expect(bannerFromJson({})).toBeNull();
    expect(bannerFromJson({ content: "", severity: "critical" })).toBeNull();
  });

  it("defaults to an info banner at the top that can be dismissed", () => {
    expect(bannerFromJson({ content: "Maintenance on Sunday" })).toEqual({
      content: "Maintenance on Sunday",
      severity: "info",
      permanent: false,
      position: "top",
    });
  });

  // The console's server passes the environment through as text.
  it("reads every field as the server sends it", () => {
    expect(
      bannerFromJson({
        content: "Read-only until 18:00 UTC",
        severity: "critical",
        url: "https://status.example.com",
        permanent: "true",
        position: "Bottom",
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
      bannerFromJson({ content: "x", position: "sidebar" })?.position,
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
  const banner = bannerFromJson({ content: "x" })!;

  it("stands where it is put", () => {
    expect(bannerAt(banner, "top")).toBe(true);
    expect(bannerAt(banner, "bottom")).toBe(false);
    expect(bannerAt({ ...banner, position: "bottom" }, "top")).toBe(false);
    expect(bannerAt({ ...banner, position: "both" }, "top")).toBe(true);
    expect(bannerAt({ ...banner, position: "both" }, "bottom")).toBe(true);
  });
});
