import { describe, expect, it, vi } from "vitest";

import { onSessionEnded, signalSessionEnded } from "./ended";

describe("session ended signal", () => {
  it("reaches a listener until it unsubscribes", () => {
    const target = new EventTarget();
    const heard = vi.fn();
    const off = onSessionEnded(heard, target);
    signalSessionEnded(target);
    expect(heard).toHaveBeenCalledTimes(1);
    off();
    signalSessionEnded(target);
    expect(heard).toHaveBeenCalledTimes(1);
  });
});
