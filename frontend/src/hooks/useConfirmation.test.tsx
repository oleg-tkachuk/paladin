import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { CONFIRMATION_MS, useConfirmation } from "./useConfirmation";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("useConfirmation", () => {
  it("shows once confirmed, and goes by itself", () => {
    const { result } = renderHook(() => useConfirmation());
    expect(result.current.shown).toBe(false);
    act(() => result.current.confirm());
    expect(result.current.shown).toBe(true);
    act(() => vi.advanceTimersByTime(CONFIRMATION_MS - 1));
    expect(result.current.shown).toBe(true);
    act(() => vi.advanceTimersByTime(1));
    expect(result.current.shown).toBe(false);
  });

  it("stays for the time it is given", () => {
    const { result } = renderHook(() => useConfirmation(CONFIRMATION_MS * 2));
    act(() => result.current.confirm());
    act(() => vi.advanceTimersByTime(CONFIRMATION_MS));
    expect(result.current.shown).toBe(true);
  });
});
