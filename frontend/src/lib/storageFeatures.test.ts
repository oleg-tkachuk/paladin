import { describe, expect, it } from "vitest";

import {
  FeatureSupport,
  StorageCompatibility,
  StorageFeature,
} from "@/gen/paladin/admin/v1/types_pb";
import { compatibilityBadge, featureWarnings } from "./storageFeatures";

const probed = { seconds: BigInt(1), nanos: 0 };

const entry = (
  feature: StorageFeature,
  support: FeatureSupport,
  required: boolean,
  wasProbed = true,
) =>
  ({
    feature,
    support,
    required,
    enables: `what ${feature} enables`,
    message: "",
    checkedAt: wasProbed ? probed : undefined,
  }) as never;

describe("featureWarnings", () => {
  it("puts a missing required feature first, then a missing optional one", () => {
    const got = featureWarnings({
      compatibility: StorageCompatibility.INCOMPATIBLE,
      features: [
        entry(
          StorageFeature.ANONYMOUS_READ_POLICY,
          FeatureSupport.UNSUPPORTED,
          false,
        ),
        entry(StorageFeature.CHECKSUM_SHA256, FeatureSupport.UNSUPPORTED, true),
      ],
    });
    expect(got.map((w) => w.level)).toEqual(["error", "warning"]);
    expect(got[0].text).toContain("SHA-256 checksums is unsupported");
    expect(got[0].text).toContain("incompatible");
    expect(got[1].text).toContain(
      `Not available on this backend: what ${StorageFeature.ANONYMOUS_READ_POLICY} enables`,
    );
  });

  it("says a backend never probed has not been probed", () => {
    const got = featureWarnings({
      compatibility: StorageCompatibility.UNVERIFIED,
      features: [
        entry(
          StorageFeature.CONDITIONAL_PUT,
          FeatureSupport.UNKNOWN,
          true,
          false,
        ),
      ],
    });
    expect(got).toEqual([
      expect.objectContaining({
        level: "info",
        text: expect.stringContaining("Not probed yet"),
      }),
    ]);
  });

  it("says a probed backend left required features unknown", () => {
    const got = featureWarnings({
      compatibility: StorageCompatibility.UNVERIFIED,
      features: [
        entry(StorageFeature.CONDITIONAL_PUT, FeatureSupport.UNKNOWN, true),
      ],
    });
    expect(got).toEqual([
      expect.objectContaining({
        level: "info",
        text: expect.stringContaining("could not be probed"),
      }),
    ]);
  });

  it("has nothing to say about a backend that supports everything", () => {
    expect(
      featureWarnings({
        compatibility: StorageCompatibility.COMPATIBLE,
        features: [
          entry(StorageFeature.CONDITIONAL_PUT, FeatureSupport.SUPPORTED, true),
        ],
      }),
    ).toEqual([]);
  });
});

describe("compatibilityBadge", () => {
  it.each([
    [
      StorageCompatibility.INCOMPATIBLE,
      FeatureSupport.SUPPORTED,
      "Incompatible",
    ],
    [StorageCompatibility.COMPATIBLE, FeatureSupport.SUPPORTED, "Compatible"],
    [StorageCompatibility.COMPATIBLE, FeatureSupport.UNSUPPORTED, "Limited"],
    [StorageCompatibility.UNVERIFIED, FeatureSupport.UNSUPPORTED, "Unverified"],
  ])(
    "compatibility %s with an optional feature %s reads %s",
    (compatibility, optional, label) => {
      expect(
        compatibilityBadge({
          compatibility,
          features: [entry(StorageFeature.SERVER_SIDE_COPY, optional, false)],
        }).label,
      ).toBe(label);
    },
  );
});
