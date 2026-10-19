import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

import { FAILED_MARK, titleWithActivity } from "./tabTitle";

const op = (state: OperationState) => create(OperationSchema, { state });

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
    [
      "a failure while one runs",
      [op(OperationState.FAILED), op(OperationState.RUNNING)],
      `${FAILED_MARK} (1) Users · Paladin`,
    ],
  ])("%s", (_, ops, want) => {
    expect(titleWithActivity("Users · Paladin", ops)).toBe(want);
  });
});
