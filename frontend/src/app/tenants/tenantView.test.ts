import { describe, expect, it } from "vitest";
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { TenantSchema } from "@/gen/paladin/admin/v1/types_pb";

import { plainLabels, tenantBreakdowns, tenantStatus } from "./tenantView";

const tenant = (over: MessageInitShape<typeof TenantSchema> = {}) =>
  create(TenantSchema, { tenantId: "t", slug: "t", ...over });

describe("tenantStatus", () => {
  it("is active by default", () => {
    expect(tenantStatus(tenant())).toEqual({
      label: "Active",
      tone: "success",
    });
  });

  it("says a tenant whose notice is loud needs a look", () => {
    expect(
      tenantStatus(
        tenant({
          labels: {
            "notice.paladin.io/content": "x",
            "notice.paladin.io/severity": "critical",
          },
        }),
      ),
    ).toEqual({ label: "Critical notice", tone: "danger" });
    expect(
      tenantStatus(
        tenant({
          labels: {
            "notice.paladin.io/content": "x",
            "notice.paladin.io/severity": "warning",
          },
        }),
      ).tone,
    ).toBe("warning");
    // An info notice is news, not a state.
    expect(
      tenantStatus(tenant({ labels: { "notice.paladin.io/content": "x" } })),
    ).toEqual({ label: "Active", tone: "success" });
  });

  it("is frozen in the trash, whatever its notice", () => {
    expect(
      tenantStatus(
        tenant({
          deletedAt: timestampFromDate(new Date()),
          labels: {
            "notice.paladin.io/content": "x",
            "notice.paladin.io/severity": "critical",
          },
        }),
      ),
    ).toEqual({ label: "Frozen", tone: "suspended" });
  });
});

describe("plainLabels", () => {
  it("leaves a notice's labels out", () => {
    expect(
      plainLabels({ team: "storage", "notice.paladin.io/content": "x" }),
    ).toEqual([["team", "storage"]]);
  });
});

describe("tenantBreakdowns", () => {
  it("splits the tenants by layout, default backend and label key", () => {
    const tenants = [
      tenant({
        storageLayout: "dedicated",
        defaultBucket: "storageBackends/primary/buckets/paladin-t1",
        labels: { team: "a", env: "prod" },
      }),
      tenant({
        storageLayout: "shared",
        defaultBucket: "storageBackends/primary/buckets/pool",
        labels: { team: "b", "notice.paladin.io/content": "x" },
      }),
      tenant({ storageLayout: "" }),
    ];
    expect(tenantBreakdowns(tenants)).toEqual({
      layout: [
        { key: "Shared", count: 2 },
        { key: "Dedicated", count: 1 },
      ],
      backend: [
        { key: "primary", count: 2 },
        { key: "No default bucket", count: 1 },
      ],
      labels: [
        { key: "team", count: 2 },
        { key: "env", count: 1 },
      ],
    });
  });
});
