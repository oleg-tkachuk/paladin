import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import {
  PreviewPurgeTenantResponseSchema,
  PurgeBlockerKind,
  PurgeBlockerSchema,
} from "@/gen/paladin/admin/v1/tenant_service_pb";
import {
  PURGE_ALSO_REMOVED,
  whatBlocksPurge,
  affectedByPurge,
} from "./purgePreview";

const TOKEN = "tenants/t/apiTokens/1";
const SUB = "tenants/t/eventSubscriptions/2";

describe("affectedByPurge", () => {
  it("names the tokens and subscriptions, counts all of them and the capabilities", () => {
    const lists = affectedByPurge(
      create(PreviewPurgeTenantResponseSchema, {
        apiTokens: [{ name: TOKEN, displayName: "ci" }],
        apiTokenCount: 51n,
        eventSubscriptions: [{ name: SUB, displayName: "http" }],
        eventSubscriptionCount: 1n,
        capabilityCount: 7n,
      }),
    );
    expect(lists).toEqual([
      {
        heading: "51 API tokens: integrations using them stop at once",
        items: ["ci"],
        total: 51,
      },
      {
        heading: "1 event subscription: deliveries stop, silently",
        items: [`http · ${SUB}`],
        total: 1,
      },
      {
        heading: "Also removed",
        items: [
          "7 capabilities, with their counters and charges",
          ...PURGE_ALSO_REMOVED,
        ],
        mono: false,
      },
    ]);
  });

  it("names a token without a display name by its resource name", () => {
    const [tokens] = affectedByPurge(
      create(PreviewPurgeTenantResponseSchema, {
        apiTokens: [{ name: TOKEN }],
        apiTokenCount: 1n,
      }),
    );
    expect(tokens.items).toEqual([TOKEN]);
  });

  it("leaves out what the tenant holds none of", () => {
    expect(
      affectedByPurge(create(PreviewPurgeTenantResponseSchema, {})),
    ).toEqual([
      { heading: "Also removed", items: PURGE_ALSO_REMOVED, mono: false },
    ]);
  });
});

describe("whatBlocksPurge", () => {
  it("is null when nothing refuses the purge", () => {
    expect(whatBlocksPurge([])).toBeNull();
  });

  it("names what the tenant still owns, counted", () => {
    const text = whatBlocksPurge([
      create(PurgeBlockerSchema, {
        kind: PurgeBlockerKind.COLLECTIONS,
        count: 2n,
      }),
      create(PurgeBlockerSchema, { kind: PurgeBlockerKind.USERS, count: 1n }),
    ]);
    expect(text).toContain("2 collections, 1 user");
    expect(text).toContain("refused");
  });
});
