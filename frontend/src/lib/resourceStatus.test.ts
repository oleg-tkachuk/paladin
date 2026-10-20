import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { MoneySchema } from "@/gen/google/type/money_pb";
import { PolicyLayerSchema } from "@/gen/paladin/admin/v1/policy_service_pb";
import { TenantBudgetSchema } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import {
  BucketSchema,
  FeatureSupport,
  QuotaSchema,
  StorageCompatibility,
  StorageFeature,
  StorageFeatureSupportSchema,
} from "@/gen/paladin/admin/v1/types_pb";

import {
  backendLifecycleStatus,
  bucketPolicyStatus,
  budgetStanding,
  compatibilityStatus,
  maintenanceStatus,
  policyStanding,
  protectionStatus,
  provisionStatus,
  quotaStanding,
  replicationStatus,
  tenantHealth,
} from "./resourceStatus";

describe("tenantHealth", () => {
  // A frozen tenant is paused on purpose, not broken.
  it("reads a trashed tenant as suspended, not as a fault", () => {
    expect(tenantHealth(true)).toMatchObject({
      tone: "suspended",
      value: "Frozen",
    });
    expect(tenantHealth(false).tone).toBe("success");
  });
});

describe("quotaStanding", () => {
  const quota = (caps: object, usage: object) =>
    create(QuotaSchema, { ...caps, usage: usage as never });

  it("limits nothing without a quota or a cap", () => {
    expect(quotaStanding(null).value).toBe("Unlimited");
    expect(quotaStanding(quota({}, { totalBytes: 5n })).value).toBe(
      "Unlimited",
    );
  });

  // One full cap refuses uploads however much room the others have.
  it("stands by its fullest cap", () => {
    const q = quota(
      { maxTotalBytes: 1000n, maxObjectCount: 10n },
      { totalBytes: 100n, objectCount: 8n },
    );
    expect(quotaStanding(q)).toMatchObject({
      tone: "warning",
      value: "Near limit",
      detail: "Objects at 80% of its cap.",
    });
  });

  it("is at its limit when a cap is reached, a cap of 0 included", () => {
    expect(quotaStanding(quota({ maxObjectsPerDay: 0n }, {}))).toMatchObject({
      tone: "danger",
      value: "At limit",
    });
  });

  it("is within limits below the near threshold", () => {
    expect(
      quotaStanding(quota({ maxTotalBytes: 100n }, { totalBytes: 10n })).tone,
    ).toBe("success");
  });
});

describe("budgetStanding", () => {
  const usd = (units: bigint) =>
    create(MoneySchema, { currencyCode: "USD", units });
  const budget = (spent: bigint, cap?: bigint) =>
    create(TenantBudgetSchema, {
      spent: usd(spent),
      budgetCeiling:
        cap === undefined
          ? { case: undefined }
          : { case: "maxBudget", value: usd(cap) },
    });

  it("has nothing to stand against without a budget", () => {
    expect(budgetStanding(null)).toMatchObject({
      tone: "unknown",
      pct: null,
    });
  });

  it("is over its cap past it, near past 80% and within below", () => {
    expect(budgetStanding(budget(120n, 100n))).toMatchObject({
      tone: "danger",
      overCap: true,
      pct: 100,
    });
    expect(budgetStanding(budget(90n, 100n)).value).toBe("Near cap");
    expect(budgetStanding(budget(10n, 100n)).value).toBe("Within cap");
  });

  it("shows the spend of an uncapped budget as it is", () => {
    expect(budgetStanding(budget(10n))).toMatchObject({
      value: "Unlimited",
      capped: false,
    });
  });
});

describe("policyStanding", () => {
  const layer = (source: string, frozen = false) =>
    create(PolicyLayerSchema, { source, frozen });

  it("counts the layers, and calls out a frozen one by its source", () => {
    expect(
      policyStanding([layer("built-in"), layer("tenants/t")]),
    ).toMatchObject({ tone: "success", value: "2 layers" });
    expect(
      policyStanding([layer("built-in"), layer("tenants/t", true)]),
    ).toMatchObject({ tone: "danger", value: "Layer frozen" });
    expect(policyStanding([layer("built-in", true)]).detail).toContain(
      "built-in",
    );
  });
});

describe("provisionStatus", () => {
  it.each([
    ["", "success", "Ready"],
    ["ready", "success", "Ready"],
    ["pending", "progress", "Provisioning"],
    ["deleting", "progress", "Deleting"],
    ["failed", "danger", "Failed"],
    ["deletion_failed", "danger", "Delete failed"],
    ["mystery", "unknown", "mystery"],
  ])("%s → %s %s", (state, tone, value) => {
    expect(provisionStatus(state)).toMatchObject({ tone, value });
  });
});

describe("bucket settings", () => {
  const bucket = (b: object) => create(BucketSchema, b as never);

  it("names what protects the objects, and reads none as off", () => {
    expect(
      protectionStatus(
        bucket({
          versioning: { enabled: true },
          objectLock: { enabled: true },
        }),
      ).value,
    ).toBe("Versioned, locked");
    expect(protectionStatus(bucket({})).tone).toBe("suspended");
  });

  it("names replication's destination, and warns of one with none", () => {
    expect(
      replicationStatus(
        bucket({
          replication: {
            enabled: true,
            destinationBucket: "storageBackends/b/buckets/x",
          },
        }),
      ).detail,
    ).toBe("To storageBackends/b/buckets/x.");
    expect(
      replicationStatus(bucket({ replication: { enabled: true } })).tone,
    ).toBe("warning");
    expect(replicationStatus(bucket({})).value).toBe("Off");
  });

  it("tells an overlay from none", () => {
    expect(
      bucketPolicyStatus(
        bucket({ cedarPolicy: "permit(principal, action, resource);" }),
      ).value,
    ).toBe("Overlay set");
    expect(bucketPolicyStatus(bucket({ cedarPolicy: "  " })).value).toBe(
      "No overlay",
    );
  });
});

describe("backend facts", () => {
  const feature = (required: boolean, support: FeatureSupport) =>
    create(StorageFeatureSupportSchema, {
      feature: StorageFeature.CONDITIONAL_PUT,
      required,
      support,
    });

  it("reads the probe's compatibility in the status tones", () => {
    expect(
      compatibilityStatus({
        compatibility: StorageCompatibility.INCOMPATIBLE,
        features: [feature(true, FeatureSupport.UNSUPPORTED)],
      }).tone,
    ).toBe("danger");
    expect(
      compatibilityStatus({
        compatibility: StorageCompatibility.COMPATIBLE,
        features: [feature(false, FeatureSupport.UNSUPPORTED)],
      }),
    ).toMatchObject({ tone: "warning", value: "Limited" });
    expect(
      compatibilityStatus({
        compatibility: StorageCompatibility.UNVERIFIED,
        features: [],
      }).tone,
    ).toBe("unknown");
  });

  it("reads maintenance as a pause, not a fault", () => {
    expect(maintenanceStatus(true).tone).toBe("suspended");
    expect(maintenanceStatus(false).tone).toBe("success");
  });
});

describe("backendLifecycleStatus", () => {
  it("says what each lifecycle state lets through", () => {
    expect(
      backendLifecycleStatus({ enabled: true, readOnly: false }),
    ).toMatchObject({ tone: "success", detail: "It serves reads and writes." });
    expect(
      backendLifecycleStatus({ enabled: true, readOnly: true }),
    ).toMatchObject({ tone: "progress", value: "Draining" });
    expect(
      backendLifecycleStatus({ enabled: false, readOnly: true }),
    ).toMatchObject({ tone: "suspended", value: "Disabled" });
  });
});
