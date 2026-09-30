import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

import { useRefetchWhile } from "./useRefetchWhile";

const INTERVAL = 1_000;

describe("useRefetchWhile", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("calls fn each interval while active, and stops when it is not", () => {
    const fn = vi.fn();
    const { rerender } = renderHook(
      ({ active }) => useRefetchWhile(active, fn, INTERVAL),
      { initialProps: { active: true } },
    );

    vi.advanceTimersByTime(INTERVAL * 2);
    expect(fn).toHaveBeenCalledTimes(2);

    rerender({ active: false });
    vi.advanceTimersByTime(INTERVAL * 3);
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("does nothing while inactive", () => {
    const fn = vi.fn();
    renderHook(() => useRefetchWhile(false, fn, INTERVAL));
    vi.advanceTimersByTime(INTERVAL * 3);
    expect(fn).not.toHaveBeenCalled();
  });
});
