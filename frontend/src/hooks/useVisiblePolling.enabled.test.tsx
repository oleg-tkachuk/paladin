import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useVisiblePolling } from "./useVisiblePolling";

const INTERVAL_MS = 1_000;

afterEach(() => {
  vi.useRealTimers();
});

// A view whose Live switch is off holds still: no first call, no ticks.
describe("useVisiblePolling enabled", () => {
  it("polls while enabled and stops when switched off", () => {
    vi.useFakeTimers();
    const fn = vi.fn();
    const { rerender } = renderHook(
      ({ on }) => useVisiblePolling(fn, INTERVAL_MS, on),
      { initialProps: { on: true } },
    );
    vi.advanceTimersByTime(INTERVAL_MS * 2);
    expect(fn).toHaveBeenCalledTimes(3);

    rerender({ on: false });
    vi.advanceTimersByTime(INTERVAL_MS * 5);
    expect(fn).toHaveBeenCalledTimes(3);
  });

  it("does not call at all when it starts switched off", () => {
    vi.useFakeTimers();
    const fn = vi.fn();
    renderHook(() => useVisiblePolling(fn, INTERVAL_MS, false));
    vi.advanceTimersByTime(INTERVAL_MS * 3);
    expect(fn).not.toHaveBeenCalled();
  });

  it("keeps polling in a hidden tab only when asked to", () => {
    vi.useFakeTimers();
    Object.defineProperty(document, "hidden", {
      configurable: true,
      get: () => true,
    });
    const quiet = vi.fn();
    const watched = vi.fn();
    renderHook(() => useVisiblePolling(quiet, INTERVAL_MS));
    renderHook(() => useVisiblePolling(watched, INTERVAL_MS, true, true));
    vi.advanceTimersByTime(INTERVAL_MS * 2);
    expect(quiet).not.toHaveBeenCalled();
    expect(watched).toHaveBeenCalledTimes(3);
    Object.defineProperty(document, "hidden", {
      configurable: true,
      get: () => false,
    });
  });
});
