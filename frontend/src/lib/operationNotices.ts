// Which of the user's operations finished between two looks at the list, and
// what a browser notification says about one. Pure, so the shell's notifier
// and its tests agree on what counts as news.
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

import {
  isActive,
  operationLabel,
  operationStatus,
  outcomeLine,
  type OpStatus,
} from "./operations";

/** Each operation's status, by name, as one look at the list saw it. */
export type StatusByName = ReadonlyMap<string, OpStatus>;

export function statusesOf(ops: readonly Operation[]): StatusByName {
  return new Map(ops.map((op) => [op.name, operationStatus(op)]));
}

/**
 * The operations that were running at the last look and have finished now.
 * One seen for the first time already finished is not news: it ended before
 * this tab was watching.
 */
export function finishedSince(
  before: StatusByName,
  ops: readonly Operation[],
): Operation[] {
  return ops.filter((op) => {
    const was = before.get(op.name);
    return was !== undefined && isActive(was) && !isActive(operationStatus(op));
  });
}

/** Whether any operation is still running, so the list is worth watching. */
export function anyActive(ops: readonly Operation[]): boolean {
  return ops.some((op) => isActive(operationStatus(op)));
}

const VERB: Record<OpStatus, string> = {
  pending: "is queued",
  running: "is running",
  succeeded: "finished",
  partial: "finished with failures",
  failed: "failed",
  cancelled: "was cancelled",
};

/** What the notification for a finished operation says. */
export function noticeFor(op: Operation): { title: string; body: string } {
  return {
    title: `${operationLabel(op)} ${VERB[operationStatus(op)]}`,
    body: outcomeLine(op) ?? "",
  };
}
