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

  it("copies again to the same destination and key prefix", () => {
    const op = batch("BatchCopy", [{ object_id: FAILED, reason: "x" }]);
    op.target!.destination = "tenants/t-1/collections/archive";
    op.target!.destinationKeyPrefix = "2026/";
    expect(retryRequest(op)).toEqual({
      kind: "copy",
      parent: COLLECTION,
      names: [`${COLLECTION}/objects/${FAILED}`],
      destination: "tenants/t-1/collections/archive",
      keyPrefix: "2026/",
    });
  });

  it("updates the same tags again, replacing as it did", () => {
    const op = batch("BatchUpdateTags", [{ object_id: FAILED, reason: "x" }]);
    op.target!.tags = { stage: "done" };
    op.target!.replace = true;
    expect(retryRequest(op)).toEqual({
      kind: "tags",
      parent: COLLECTION,
      names: [`${COLLECTION}/objects/${FAILED}`],
      tags: { stage: "done" },
      replace: true,
    });
  });

  // A server that does not send the tags would have the retry ask for none,
  // which with replace clears every tag the objects have.
  it("offers nothing for a tag update whose tags it does not know", () => {
    const op = batch("BatchUpdateTags", [{ object_id: FAILED, reason: "x" }]);
    op.target!.replace = true;
    expect(retryRequest(op)).toBeNull();
  });

  it("offers nothing for a copy whose destination it does not know", () => {
    expect(
      retryRequest(batch("BatchCopy", [{ object_id: FAILED, reason: "x" }])),
    ).toBeNull();
  });
});
