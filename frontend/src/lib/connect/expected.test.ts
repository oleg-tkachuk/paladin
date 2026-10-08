import { describe, expect, it } from "vitest";
import { createContextValues } from "@connectrpc/connect";

import { kNotFoundIsAnswer, notFoundIsAnswer } from "./expected";

describe("notFoundIsAnswer", () => {
  it("marks the call and keeps the options it was given", () => {
    const signal = new AbortController().signal;
    const opts = notFoundIsAnswer({ signal, timeoutMs: 5 });
    expect(opts.signal).toBe(signal);
    expect(opts.timeoutMs).toBe(5);
    expect(opts.contextValues!.get(kNotFoundIsAnswer)).toBe(true);
  });

  it("adds to context values the caller already set", () => {
    const contextValues = createContextValues();
    const opts = notFoundIsAnswer({ contextValues });
    expect(opts.contextValues).toBe(contextValues);
    expect(contextValues.get(kNotFoundIsAnswer)).toBe(true);
  });

  it("leaves an unmarked call unmarked", () => {
    expect(createContextValues().get(kNotFoundIsAnswer)).toBe(false);
  });
});
