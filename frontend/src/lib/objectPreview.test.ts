import { describe, expect, it } from "vitest";

import {
  TEXT_PREVIEW_MAX_BYTES,
  mediaType,
  previewKind,
} from "./objectPreview";

const SMALL = BigInt(1);
const LIMIT = BigInt(TEXT_PREVIEW_MAX_BYTES);

describe("previewKind", () => {
  it.each([
    ["image/png", SMALL, "image"],
    ["image/svg+xml", SMALL, "image"],
    ["IMAGE/JPEG", SMALL, "image"],
    ["application/pdf", LIMIT * BigInt(100), "pdf"],
    ["text/plain; charset=utf-8", SMALL, "text"],
    ["text/html", SMALL, "text"],
    ["application/json", LIMIT, "text"],
    ["text/csv", LIMIT + SMALL, null],
    ["image/heic", SMALL, null],
    ["image/tiff", SMALL, null],
    ["application/octet-stream", SMALL, null],
    ["", SMALL, null],
  ] as const)("%s of %s bytes shows as %s", (type, size, want) => {
    expect(previewKind(type, size)).toBe(want);
  });
});

describe("mediaType", () => {
  it("drops the parameters and the case", () => {
    expect(mediaType(" Text/Plain ; charset=UTF-8")).toBe("text/plain");
  });
});
