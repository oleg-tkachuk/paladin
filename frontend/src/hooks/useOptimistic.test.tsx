import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { edit } from "@/lib/optimistic";

import { useOptimistic } from "./useOptimistic";

describe("useOptimistic", () => {
  // A component rendered alone, as in a test, has no cache to edit.
  it("runs the change with no query client to edit", async () => {
    const { result } = renderHook(() => useOptimistic());
    await expect(
      result.current([edit<number>(["n"], (n) => n + 1)], async () => "done"),
    ).resolves.toBe("done");
  });
});
