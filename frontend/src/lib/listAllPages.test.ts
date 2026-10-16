import { describe, expect, it, vi } from "vitest";

import { MAX_LIST_PAGES } from "@/hooks/useBuckets";

import { listAllPages } from "./listAllPages";

describe("listAllPages", () => {
  it("follows the token to the end", async () => {
    const page = vi
      .fn()
      .mockResolvedValueOnce({ items: [1, 2], next: "p2" })
      .mockResolvedValueOnce({ items: [3], next: "" });
    expect(await listAllPages(page)).toEqual([1, 2, 3]);
    expect(page.mock.calls.map((c) => c[0])).toEqual(["", "p2"]);
  });

  it("stops at the ceiling rather than paging forever", async () => {
    const page = vi.fn().mockResolvedValue({ items: [0], next: "again" });
    await listAllPages(page);
    expect(page).toHaveBeenCalledTimes(MAX_LIST_PAGES);
  });

  it("passes a failed page's error on", async () => {
    const page = vi.fn().mockRejectedValue(new Error("unavailable"));
    await expect(listAllPages(page)).rejects.toThrow("unavailable");
  });
});
