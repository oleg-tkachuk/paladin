import { describe, expect, it } from "vitest";

import { CONSOLE_USER_AGENT_PRODUCT } from "@/constants";

import { parseAuditClient } from "./client";

describe("parseAuditClient", () => {
  it.each([
    [
      "an application through the Go SDK",
      "paladin-sdk-go/0.97.0 gateway/1.4",
      { name: "gateway/1.4", via: "Go SDK 0.97.0", kind: "paladin" },
    ],
    [
      "the Python SDK on its own",
      "paladin-sdk-python/0.97.0",
      { name: "Python SDK 0.97.0", via: "", kind: "paladin" },
    ],
    [
      "a suffix of several products",
      "paladin-sdk-go/0.97.0 rag-api/2 worker",
      { name: "rag-api/2 worker", via: "Go SDK 0.97.0", kind: "paladin" },
    ],
    [
      "the console",
      `${CONSOLE_USER_AGENT_PRODUCT}/39.2.1`,
      { name: "Console 39.2.1", via: "", kind: "paladin" },
    ],
    [
      "a browser",
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
      { name: "Browser", via: "", kind: "browser" },
    ],
    [
      "another client",
      "curl/8.7.1",
      { name: "curl 8.7.1", via: "", kind: "other" },
    ],
    [
      "a product without a version",
      "  grpcurl  ",
      { name: "grpcurl", via: "", kind: "other" },
    ],
  ])("reads %s", (_, ua, want) => {
    expect(parseAuditClient(ua)).toEqual(want);
  });

  it("says nothing of a call that sent no User-Agent", () => {
    expect(parseAuditClient("")).toBeNull();
    expect(parseAuditClient("   ")).toBeNull();
  });
});
