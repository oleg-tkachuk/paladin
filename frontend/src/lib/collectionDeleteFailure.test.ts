import { describe, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import { PALADIN_ERROR_DOMAIN } from "@/hooks/errorContract";

import {
  collectionDeleteFailure,
  collectionHolding,
} from "./collectionDeleteFailure";

const NOT_EMPTY = "ERROR_REASON_COLLECTION_NOT_EMPTY";

function refused(reason: string, metadata: Record<string, string>) {
  return new ConnectError(
    "collection still holds 1 object in its trash; purge or restore them first",
    Code.FailedPrecondition,
    undefined,
    [
      {
        desc: ErrorInfoSchema,
        value: create(ErrorInfoSchema, {
          domain: PALADIN_ERROR_DOMAIN,
          reason,
          metadata,
        }),
      },
    ],
  );
}

describe("collectionHolding", () => {
  it.each([
    [
      { live_objects: "2", trashed_objects: "1" },
      { live: 2, trashed: 1 },
    ],
    [
      { live_objects: "0", trashed_objects: "3" },
      { live: 0, trashed: 3 },
    ],
    [{ live_objects: "2" }, null],
    [{ live_objects: "two", trashed_objects: "1" }, null],
    [{}, null],
  ])("%o → %o", (md, want) => {
    expect(collectionHolding(refused(NOT_EMPTY, md))).toEqual(want);
  });

  it("reads nothing off another refusal", () => {
    expect(
      collectionHolding(
        refused("ERROR_REASON_VERSION_CONFLICT", {
          live_objects: "1",
          trashed_objects: "1",
        }),
      ),
    ).toBeNull();
  });
});

describe("collectionDeleteFailure", () => {
  const target = { tenantId: "acme", collection: "docs/2026" };

  it("offers the trash when the trash holds the collection", () => {
    const open = vi.fn();
    const n = collectionDeleteFailure(
      refused(NOT_EMPTY, { live_objects: "0", trashed_objects: "1" }),
      target,
      open,
    );
    expect(n.message).toMatch(/in its trash/);
    n.action?.onClick();
    expect(open).toHaveBeenCalledWith(
      "/tenants/acme/collections/docs%2F2026/trash",
    );
  });

  it("offers nothing when only live objects hold it, or nothing said what", () => {
    const open = vi.fn();
    expect(
      collectionDeleteFailure(
        refused(NOT_EMPTY, { live_objects: "2", trashed_objects: "0" }),
        target,
        open,
      ).action,
    ).toBeUndefined();
    expect(
      collectionDeleteFailure(new Error("network"), target, open).action,
    ).toBeUndefined();
  });
});
