import { describe, expect, it } from "vitest";

import { tallyBy, viewModeCodec } from "./viewMode";

describe("viewModeCodec", () => {
  const codec = viewModeCodec("table");
  const read = (qs: string) => codec.read(new URLSearchParams(qs));

  it("reads the view a link names", () => {
    expect(read("view=tiles")).toBe("tiles");
    expect(read("q=acme&view=summary")).toBe("summary");
  });

  // An old link, or one typed by hand, opens the list rather than failing.
  it("reads no view, or one it does not offer, as the fallback", () => {
    expect(read("")).toBe("table");
    expect(read("view=kanban")).toBe("table");
    expect(viewModeCodec("tiles").read(new URLSearchParams(""))).toBe("tiles");
  });

  it("leaves the fallback out of the URL", () => {
    expect(codec.write("table")).toEqual({ view: null });
    expect(codec.write("summary")).toEqual({ view: "summary" });
    // The fallback is the user's saved view; the table is then written out.
    expect(viewModeCodec("tiles").write("table")).toEqual({ view: "table" });
  });
});

describe("tallyBy", () => {
  const rows = [{ p: "aws" }, { p: "garage" }, { p: "aws" }, { p: "" }, {}];

  it("counts each value, most first, the rest by name", () => {
    expect(tallyBy(rows, (r: { p?: string }) => r.p, "Not known")).toEqual([
      { key: "aws", count: 2 },
      { key: "Not known", count: 2 },
      { key: "garage", count: 1 },
    ]);
  });

  it("is empty for no rows", () => {
    expect(tallyBy([], () => "x", "none")).toEqual([]);
  });
});
