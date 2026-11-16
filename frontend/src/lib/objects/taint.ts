// An object's taint signals (data/v1 TaintSignal): reasons its content must
// not reach an agent whose capability does not allow tainted reads. One
// description of each signal, which every view of a taint reads, so a badge,
// a card and a confirmation name the same thing the same way.

import { TaintSignal } from "@/gen/paladin/data/v1/types_pb";

export interface TaintSignalInfo {
  signal: TaintSignal;
  label: string;
  description: string;
}

/** Every signal the API defines, in the order the console shows them. */
export const TAINT_SIGNALS: readonly TaintSignalInfo[] = [
  {
    signal: TaintSignal.PROMPT_INJECTION,
    label: "Prompt injection",
    description: "Text that tries to instruct the model reading it.",
  },
  {
    signal: TaintSignal.PII,
    label: "Personal data",
    description: "Data about a person.",
  },
  {
    signal: TaintSignal.SECRETS,
    label: "Secrets",
    description: "A credential, key or token.",
  },
];

const ORDER = new Map(TAINT_SIGNALS.map((s, i) => [s.signal, i]));
const LABEL = new Map(TAINT_SIGNALS.map((s) => [s.signal, s.label]));

/** An object's signals, each once, in the canonical order. */
export function taintSignalsOf(
  object: { taint: readonly TaintSignal[] } | null | undefined,
): TaintSignal[] {
  const known = new Set((object?.taint ?? []).filter((s) => ORDER.has(s)));
  return [...known].sort((a, b) => ORDER.get(a)! - ORDER.get(b)!);
}

/** The signals' names, joined for a badge or a sentence; "" for none. */
export function taintLabel(signals: readonly TaintSignal[]): string {
  return signals.map((s) => LABEL.get(s) ?? String(s)).join(", ");
}

/**
 * The signals in current that next no longer has. Clearing one re-opens the
 * object to agents not allowed tainted content, which is why a save that
 * clears any asks first.
 */
export function clearedBy(
  current: readonly TaintSignal[],
  next: readonly TaintSignal[],
): TaintSignal[] {
  const kept = new Set(next);
  return current.filter((s) => !kept.has(s));
}
