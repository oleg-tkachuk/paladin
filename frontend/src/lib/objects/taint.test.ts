import { describe, expect, it } from "vitest";

import { TaintSignal } from "@/gen/paladin/data/v1/types_pb";

import { TAINT_SIGNALS, clearedBy, taintLabel, taintSignalsOf } from "./taint";

describe("TAINT_SIGNALS", () => {
  it("describes every signal the API defines, and nothing else", () => {
    const defined = Object.values(TaintSignal).filter(
      (v): v is TaintSignal =>
        typeof v === "number" && v !== TaintSignal.UNSPECIFIED,
    );
    expect(TAINT_SIGNALS.map((s) => s.signal).sort()).toEqual(defined.sort());
    for (const s of TAINT_SIGNALS) {
      expect(s.label).not.toBe("");
      expect(s.description).not.toBe("");
    }
  });
});

describe("taintSignalsOf", () => {
  it("is empty for an object that carries no signal", () => {
    expect(taintSignalsOf({ taint: [] })).toEqual([]);
    expect(taintSignalsOf(null)).toEqual([]);
  });

  it("drops the unspecified value and keeps the canonical order, once each", () => {
    expect(
      taintSignalsOf({
        taint: [
          TaintSignal.SECRETS,
          TaintSignal.UNSPECIFIED,
          TaintSignal.PROMPT_INJECTION,
          TaintSignal.SECRETS,
        ],
      }),
    ).toEqual([TaintSignal.PROMPT_INJECTION, TaintSignal.SECRETS]);
  });
});

describe("taintLabel", () => {
  it("names each signal, in the order given", () => {
    expect(taintLabel([TaintSignal.PII, TaintSignal.SECRETS])).toBe(
      "Personal data, Secrets",
    );
    expect(taintLabel([])).toBe("");
  });
});

// Clearing a signal re-opens the object to agents not allowed tainted
// content, so the console asks before a save that clears one.
describe("clearedBy", () => {
  it("names the signals a change removes", () => {
    expect(
      clearedBy(
        [TaintSignal.PII, TaintSignal.SECRETS],
        [TaintSignal.SECRETS, TaintSignal.PROMPT_INJECTION],
      ),
    ).toEqual([TaintSignal.PII]);
  });

  it("is empty for a change that only adds", () => {
    expect(
      clearedBy([TaintSignal.PII], [TaintSignal.PII, TaintSignal.SECRETS]),
    ).toEqual([]);
    expect(clearedBy([], [TaintSignal.PII])).toEqual([]);
  });
});
