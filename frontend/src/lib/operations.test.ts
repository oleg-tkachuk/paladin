import { describe, expect, it } from "vitest";
import { create, fromJson } from "@bufbuild/protobuf";
import {
  anyPack,
  StructSchema,
  timestampFromDate,
} from "@bufbuild/protobuf/wkt";

import {
  OperationSchema,
  type Operation,
} from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

import {
  isActive,
  operationElapsedMs,
  operationLabel,
  operationOutcome,
  operationStatus,
  parseCollectionName,
} from "./operations";

const op = (o: Partial<Operation> = {}): Operation =>
  create(OperationSchema, o as never);

const response = (json: Record<string, unknown>) => ({
  case: "response" as const,
  value: anyPack(StructSchema, fromJson(StructSchema, json as never)),
});

describe("operationStatus", () => {
  it.each([
    [OperationState.PENDING, "pending"],
    [OperationState.RUNNING, "running"],
    [OperationState.SUCCEEDED, "succeeded"],
    [OperationState.FAILED, "failed"],
    [OperationState.CANCELLED, "cancelled"],
  ] as const)("reads state %s as %s", (state, want) => {
    expect(operationStatus(op({ state }))).toBe(want);
  });

  // A server that sends no state is read the old way.
  it("falls back on done and the result", () => {
    expect(operationStatus(op({ done: false }))).toBe("running");
    expect(operationStatus(op({ done: true }))).toBe("succeeded");
    expect(
      operationStatus(
        op({
          done: true,
          result: {
            case: "error",
            value: { code: 13, message: "x", details: [] },
          } as never,
        }),
      ),
    ).toBe("failed");
  });

  it("counts only queued and running work as stoppable", () => {
    expect(["pending", "running"].every((s) => isActive(s as never))).toBe(
      true,
    );
    expect(
      ["succeeded", "failed", "cancelled"].some((s) => isActive(s as never)),
    ).toBe(false);
  });
});

describe("operationLabel", () => {
  it.each([
    [{ type: "BatchDelete" }, "Move to trash"],
    [
      { type: "BatchDelete", target: { permanent: true } },
      "Delete permanently",
    ],
    [{ type: "BatchUpdateTags" }, "Merge tags"],
    [{ type: "BatchUpdateTags", target: { replace: true } }, "Replace tags"],
    [{ type: "BatchCopy" }, "Copy objects"],
    [{ type: "BatchRestoreObjects" }, "Restore from trash"],
    [{ type: "MigrateTenantStorageLayout" }, "Migrate tenant storage layout"],
    [{ type: "" }, "Operation"],
  ])("%j reads %j", (o, want) => {
    expect(operationLabel(op(o as never))).toBe(want);
  });
});

describe("parseCollectionName", () => {
  it("splits a collection's name, a slash in the collection kept", () => {
    expect(parseCollectionName("tenants/t-1/collections/e2e/docs")).toEqual({
      tenant: "t-1",
      collection: "e2e/docs",
    });
  });
  it.each(["", "buckets/b", "tenants/t-1", undefined])("refuses %j", (n) => {
    expect(parseCollectionName(n)).toBeNull();
  });
});

describe("operationOutcome", () => {
  it("reads the counts and each failure a batch reported", () => {
    const o = op({
      result: response({
        total: 3,
        succeeded: 1,
        failed: 1,
        already_applied: 1,
        failures: [{ object_id: "o-2", reason: "locked" }],
      }) as never,
    });
    expect(operationOutcome(o)).toEqual({
      total: 3,
      succeeded: 1,
      failed: 1,
      alreadyApplied: 1,
      failures: [{ objectId: "o-2", reason: "locked" }],
    });
  });

  it("has none for an operation that reported none", () => {
    expect(operationOutcome(op())).toBeNull();
    expect(
      operationOutcome(op({ result: response({ note: "x" }) as never })),
    ).toBeNull();
  });
});

describe("operationElapsedMs", () => {
  const start = new Date("2026-10-18T10:00:00Z");
  const now = new Date("2026-10-18T10:05:00Z");
  it("measures a finished operation to when it finished", () => {
    const o = op({
      createdAt: timestampFromDate(start),
      doneAt: timestampFromDate(new Date("2026-10-18T10:00:42Z")),
      done: true,
    });
    expect(operationElapsedMs(o, now)).toBe(42_000);
  });
  it("measures a running one to now", () => {
    expect(
      operationElapsedMs(op({ createdAt: timestampFromDate(start) }), now),
    ).toBe(300_000);
  });
  it("cannot measure a finished one that says not when", () => {
    expect(
      operationElapsedMs(
        op({ createdAt: timestampFromDate(start), done: true }),
        now,
      ),
    ).toBeNull();
  });
});
