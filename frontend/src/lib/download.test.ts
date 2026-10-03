import { describe, expect, it } from "vitest";

import { attachmentDisposition } from "./download";

describe("attachmentDisposition", () => {
  it.each([
    ["a plain name", "report.pdf", "attachment; filename*=UTF-8''report.pdf"],
    [
      "the last segment of a key",
      "docs/2026/report.pdf",
      "attachment; filename*=UTF-8''report.pdf",
    ],
    [
      "a name RFC 5987 must escape",
      "it's (1)!.txt",
      "attachment; filename*=UTF-8''it%27s%20%281%29%21.txt",
    ],
    [
      "a non-ASCII name",
      "звіт.pdf",
      "attachment; filename*=UTF-8''%D0%B7%D0%B2%D1%96%D1%82.pdf",
    ],
    ["a key with no name", "dir/", "attachment; filename*=UTF-8''dir"],
    ["an empty key", "", "attachment"],
  ])("%s", (_, key, want) => {
    expect(attachmentDisposition(key)).toBe(want);
  });
});
