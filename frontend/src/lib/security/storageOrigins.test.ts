import { describe, expect, it } from "vitest";

import { parseStorageOrigins } from "./storageOrigins";

describe("parseStorageOrigins", () => {
  it.each([
    ["nothing set", undefined, []],
    ["an empty list", " , ", []],
    ["one origin", "https://s3.paladin.local", ["https://s3.paladin.local"]],
    [
      "several, trimmed, with a port",
      " https://s3.example.com , http://localhost:9000 ",
      ["https://s3.example.com", "http://localhost:9000"],
    ],
    ["a trailing slash", "https://s3.example.com/", ["https://s3.example.com"]],
    ["upper case", "HTTPS://S3.Example.com", ["https://s3.example.com"]],
    ["a repeat once", "https://a.test,https://a.test", ["https://a.test"]],
  ])("keeps %s", (_, raw, want) => {
    expect(parseStorageOrigins(raw)).toEqual({ origins: want, invalid: [] });
  });

  it.each([
    ["a path", "https://s3.example.com/bucket"],
    ["a query", "https://s3.example.com?x=1"],
    ["a wildcard", "https://*.example.com"],
    ["no scheme", "s3.example.com"],
    ["another scheme", "ftp://s3.example.com"],
    ["a bare keyword", "*"],
  ])("drops and reports %s", (_, raw) => {
    expect(parseStorageOrigins(`https://ok.test,${raw}`)).toEqual({
      origins: ["https://ok.test"],
      invalid: [raw],
    });
  });
});
