import { describe, expect, it } from "vitest";
import { create, fromJson } from "@bufbuild/protobuf";
import { anyPack, StructSchema } from "@bufbuild/protobuf/wkt";

import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

import { retryRequest } from "./operationRetry";

const COLLECTION = "tenants/t-1/collections/docs";
const FAILED = "01a15875-a5aa-71f4-b0f9-4e7bb7b537b6";

const batch = (type: string, failures: object[], permanent = false) =>
  create(OperationSchema, {
    type,
    state: OperationState.SUCCEEDED,
    failedCount: BigInt(failures.length),
    target: { collection: COLLECTION, permanent, objectCount: 2 },
    result: {
      case: "response",
      value: anyPack(
        StructSchema,
        fromJson(StructSchema, {
          total: 2,
          succeeded: 2 - failures.length,
          failed: failures.length,
          failures,
        } as never),
      ),
    },
  });

describe("retryRequest", () => {
  it("deletes again the objects a delete could not, as it was asked", () => {
    const failures = [
      { object_id: FAILED, reason: "statemachine: version conflict" },
    ];
    expect(retryRequest(batch("BatchDelete", failures, true))).toEqual({
      kind: "delete",
      parent: COLLECTION,
      names: [`${COLLECTION}/objects/${FAILED}`],
      permanent: true,
    });
  });

  it("restores again the objects a restore could not", () => {
    expect(
      retryRequest(
        batch("BatchRestoreObjects", [{ object_id: FAILED, reason: "x" }]),
      ),
    ).toEqual({
      kind: "restore",
      parent: COLLECTION,
      names: [`${COLLECTION}/objects/${FAILED}`],
    });
  });

  it("offers nothing for a batch that left nothing behind", () => {
    expect(retryRequest(batch("BatchDelete", []))).toBeNull();
  });

  // A copy's key prefix and a tag update's tags are not on the operation, so
  // a retry could not ask for what they asked.
  it("offers nothing for a copy or a tag update", () => {
    const failures = [{ object_id: FAILED, reason: "x" }];
    expect(retryRequest(batch("BatchCopy", failures))).toBeNull();
    expect(retryRequest(batch("BatchUpdateTags", failures))).toBeNull();
  });
});
