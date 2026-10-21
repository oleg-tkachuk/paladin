import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

import { SEARCH_DEBOUNCE_MS, useDebouncedValue } from "./useDebouncedValue";

describe("useDebouncedValue", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("starts on the value it is given", () => {
    const { result } = renderHook(() => useDebouncedValue("a"));
    expect(result.current).toBe("a");
  });

  it("follows a burst of changes once it settles, not on each", () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v), {
      initialProps: { v: "a" },
    });
    rerender({ v: "ab" });
    act(() => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS - 1));
    rerender({ v: "abc" });
    act(() => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS - 1));
    expect(result.current).toBe("a");
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe("abc");
  });
});
