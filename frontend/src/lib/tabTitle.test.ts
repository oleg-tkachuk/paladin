import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

import { FAILED_MARK, activitySignal, titleWithActivity } from "./tabTitle";

const op = (state: OperationState) => create(OperationSchema, { state });
// A batch that ran to its end leaving an object behind.
const partial = () =>
  create(OperationSchema, { state: OperationState.SUCCEEDED, failedCount: 1n });

describe("titleWithActivity", () => {
  it.each([
    ["nothing running", [], "Users · Paladin"],
    [
      "two running and one done",
      [
        op(OperationState.RUNNING),
        op(OperationState.PENDING),
        op(OperationState.SUCCEEDED),
      ],
      "(2) Users · Paladin",
    ],
    [
      "a failure",
      [op(OperationState.FAILED)],
      `${FAILED_MARK} Users · Paladin`,
    ],
    // Objects left behind are a failure to look at too.
    ["a partly failed batch", [partial()], `${FAILED_MARK} Users · Paladin`],
    [
      "a failure while one runs",
      [op(OperationState.FAILED), op(OperationState.RUNNING)],
      `${FAILED_MARK} (1) Users · Paladin`,
    ],
  ])("%s", (_, ops, want) => {
    expect(titleWithActivity("Users · Paladin", ops)).toBe(want);
  });
});

describe("activitySignal", () => {
  it.each([
    ["no operations", [], "idle"],
    ["only finished ones", [op(OperationState.SUCCEEDED)], "idle"],
    ["a partly failed batch", [partial()], "failed"],
    ["one running", [op(OperationState.RUNNING)], "running"],
    ["one queued", [op(OperationState.PENDING)], "running"],
    [
      "a failure beside running work",
      [op(OperationState.RUNNING), op(OperationState.FAILED)],
      "failed",
    ],
  ])("%s → %s", (_, ops, want) => {
    expect(activitySignal(ops)).toBe(want);
  });
});
