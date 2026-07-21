import { describe, it, expect, vi, afterEach } from "vitest";

import { copyToClipboard } from "./utils";

// copyToClipboard touches navigator.clipboard / window.isSecureContext /
// document.execCommand — this file is .tsx so it runs under jsdom.

function setSecureContext(v: boolean) {
  Object.defineProperty(window, "isSecureContext", {
    value: v,
    configurable: true,
  });
}

function setClipboard(writeText: unknown) {
  Object.defineProperty(navigator, "clipboard", {
    value: writeText === undefined ? undefined : { writeText },
    configurable: true,
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("copyToClipboard", () => {
  it("uses the async Clipboard API in a secure context", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setSecureContext(true);
    setClipboard(writeText);

    await expect(copyToClipboard("hello")).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith("hello");
  });

  it("falls back to execCommand when not in a secure context", async () => {
    setSecureContext(false);
    setClipboard(undefined);
    // jsdom doesn't implement execCommand, so assign a mock directly
    // (spyOn requires the property to already exist).
    const exec = vi.fn().mockReturnValue(true);
    (document as unknown as { execCommand: typeof exec }).execCommand = exec;

    await expect(copyToClipboard("via-fallback")).resolves.toBe(true);
    expect(exec).toHaveBeenCalledWith("copy");
    // the temporary textarea must be cleaned up
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("returns false when the clipboard write throws", async () => {
    setSecureContext(true);
    setClipboard(vi.fn().mockRejectedValue(new Error("denied")));

    await expect(copyToClipboard("nope")).resolves.toBe(false);
  });
});
