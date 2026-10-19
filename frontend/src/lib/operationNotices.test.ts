import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

import {
  anyActive,
  finishedSince,
  noticeFor,
  statusesOf,
} from "./operationNotices";

const op = (name: string, state: OperationState) =>
  create(OperationSchema, {
    name,
    state,
    done: state > OperationState.RUNNING,
  });

describe("finishedSince", () => {
  it("names what was running and is done now", () => {
    const before = statusesOf([
      op("a", OperationState.RUNNING),
      op("b", OperationState.PENDING),
      op("c", OperationState.RUNNING),
    ]);
    const now = [
      op("a", OperationState.SUCCEEDED),
      op("b", OperationState.FAILED),
      op("c", OperationState.RUNNING),
    ];
    expect(finishedSince(before, now).map((o) => o.name)).toEqual(["a", "b"]);
  });

  it("is not news when an operation is first seen already finished", () => {
    expect(
      finishedSince(new Map(), [op("a", OperationState.SUCCEEDED)]),
    ).toEqual([]);
  });

  it("does not repeat a finish it already reported", () => {
    const before = statusesOf([op("a", OperationState.FAILED)]);
    expect(finishedSince(before, [op("a", OperationState.FAILED)])).toEqual([]);
  });
});

describe("anyActive and noticeFor", () => {
  it("tells a list still worth watching", () => {
    expect(anyActive([op("a", OperationState.SUCCEEDED)])).toBe(false);
    expect(anyActive([op("a", OperationState.PENDING)])).toBe(true);
  });
  it("says how it ended", () => {
    expect(noticeFor(op("a", OperationState.FAILED)).title).toMatch(/failed$/);
    expect(noticeFor(op("a", OperationState.SUCCEEDED)).title).toMatch(
      /finished$/,
    );
  });
});
