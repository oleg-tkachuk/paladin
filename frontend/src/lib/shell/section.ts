import { ConnectError, Code } from "@connectrpc/connect";

import type { ShellSection } from "@/context/ShellContext";

/** Why a section could not be read, as the chrome's tooltip says it. */
export function sectionReason(err: unknown): string {
  if (err instanceof ConnectError)
    return `${Code[err.code]}: ${err.rawMessage}`;
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * One shell section from its settled call: the value, or unavailable with
 * the reason it failed. pick narrows an answer to what the section shows.
 */
export function settledSection<R, T = R>(
  settled: PromiseSettledResult<R>,
  pick: (value: R) => T = (value) => value as unknown as T,
): ShellSection<T> {
  if (settled.status === "rejected") {
    return {
      status: "unavailable",
      reason: sectionReason(settled.reason),
      data: null,
    };
  }
  return { status: "ok", data: pick(settled.value) };
}
