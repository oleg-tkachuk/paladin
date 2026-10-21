// What a background tab's title says about the user's operations: how many
// are still running, and a mark while one has failed and not been cleared.
// Pure, so the shell and its tests agree on the words.
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

import { hasFailures, isActive, operationStatus } from "./operations";

/**
 * Set before the title while one of the user's operations has failed, or
 * left objects behind.
 */
export const FAILED_MARK = "⚠";

/** What the tab's icon shows: a failure outranks work still running. */
export type ActivitySignal = "idle" | "running" | "failed";

export function activitySignal(ops: readonly Operation[]): ActivitySignal {
  const statuses = ops.map(operationStatus);
  if (statuses.some(hasFailures)) return "failed";
  return statuses.some(isActive) ? "running" : "idle";
}

export function titleWithActivity(
  title: string,
  ops: readonly Operation[],
): string {
  const statuses = ops.map(operationStatus);
  const running = statuses.filter(isActive).length;
  const failed = statuses.some(hasFailures);
  const parts = [
    failed ? FAILED_MARK : "",
    running > 0 ? `(${running})` : "",
    title,
  ];
  return parts.filter(Boolean).join(" ");
}
