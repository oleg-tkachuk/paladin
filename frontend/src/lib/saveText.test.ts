// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

import { saveText } from "./saveText";

describe("saveText", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("clicks a link to the text under the file name, then lets the URL go", async () => {
    vi.useFakeTimers();
    const blobs: Blob[] = [];
    URL.createObjectURL = vi.fn((b: Blob) => {
      blobs.push(b);
      return "blob:x";
    });
    URL.revokeObjectURL = vi.fn();
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicked.push(this);
    });

    saveText("a,b\r\n", "users.csv", "text/csv;charset=utf-8");

    expect(clicked).toHaveLength(1);
    expect(clicked[0].download).toBe("users.csv");
    expect(clicked[0].getAttribute("href")).toBe("blob:x");
    expect(clicked[0].isConnected).toBe(false);
    expect(await blobs[0].text()).toBe("a,b\r\n");
    expect(blobs[0].type).toBe("text/csv;charset=utf-8");
    expect(URL.revokeObjectURL).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:x");
  });
});
