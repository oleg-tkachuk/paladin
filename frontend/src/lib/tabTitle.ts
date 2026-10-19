// What a background tab's title says about the user's operations: how many
// are still running, and a mark while one has failed and not been cleared.
// Pure, so the shell and its tests agree on the words.
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

import { isActive, operationStatus } from "./operations";

/** Set before the title while one of the user's operations has failed. */
export const FAILED_MARK = "⚠";

export function titleWithActivity(
  title: string,
  ops: readonly Operation[],
): string {
  const statuses = ops.map(operationStatus);
  const running = statuses.filter(isActive).length;
  const failed = statuses.includes("failed");
  const parts = [
    failed ? FAILED_MARK : "",
    running > 0 ? `(${running})` : "",
    title,
  ];
  return parts.filter(Boolean).join(" ");
}
