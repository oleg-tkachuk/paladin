import { describe, expect, it } from "vitest";

import {
  buildObjectFilter,
  contentTypeClause,
  parseKeyValue,
} from "./objectFilter";

describe("buildObjectFilter", () => {
  it("is empty with nothing set", () => {
    expect(buildObjectFilter({})).toBe("");
  });

  it("joins every clause with && — one indexed query on the server", () => {
    expect(
      buildObjectFilter({
        status: "AVAILABLE",
        search: "report",
        tag: "env=prod",
        type: "image",
        meta: "owner=ops",
      }),
    ).toBe(
      'state == "AVAILABLE" && key.contains("report") && tags["env"] == "prod"' +
        ' && content_type.startsWith("image/") && metadata["owner"] == "ops"',
    );
  });

  it("applies search from three characters", () => {
    expect(buildObjectFilter({ search: "ab" })).toBe("");
    expect(buildObjectFilter({ search: "abc" })).toBe('key.contains("abc")');
  });

  it("uses equality for exact content types", () => {
    expect(contentTypeClause("pdf")).toBe('content_type == "application/pdf"');
  });

  it("ignores a content type it does not offer instead of splicing it in", () => {
    expect(contentTypeClause('image" || true || "')).toBeUndefined();
    expect(buildObjectFilter({ type: "x) || true" })).toBe("");
  });

  it("escapes both halves of the metadata facet", () => {
    expect(buildObjectFilter({ meta: 'k"=v" || true || "' })).toBe(
      'metadata["k\\""] == "v\\" || true || \\""',
    );
  });
});

describe("parseKeyValue", () => {
  it("splits on the first =", () => {
    expect(parseKeyValue("q=a=b")).toEqual({ key: "q", value: "a=b" });
  });
  it("allows an empty value", () => {
    expect(parseKeyValue("k=")).toEqual({ key: "k", value: "" });
  });
  it("rejects a missing key or separator", () => {
    expect(parseKeyValue("=v")).toBeUndefined();
    expect(parseKeyValue("novalue")).toBeUndefined();
    expect(parseKeyValue("")).toBeUndefined();
  });
  it("keeps both halves exactly", () => {
    expect(parseKeyValue(" owner = ops ")).toEqual({
      key: " owner ",
      value: " ops ",
    });
  });
});
