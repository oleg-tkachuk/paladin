import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";

import {
  FeatureSupport,
  StorageCompatibility,
  StorageFeature,
} from "@/gen/paladin/admin/v1/types_pb";
import { BackendFeatures } from "./BackendFeatures";

const probed = { seconds: BigInt(1), nanos: 0 };

describe("BackendFeatures", () => {
  it("lists every feature with its result, and the warning a gap adds up to", () => {
    render(
      <BackendFeatures
        backend={
          {
            compatibility: StorageCompatibility.COMPATIBLE,
            features: [
              {
                feature: StorageFeature.CONDITIONAL_PUT,
                support: FeatureSupport.SUPPORTED,
                required: true,
                enables:
                  "refusing an upload that would replace an existing object",
                message: "",
                checkedAt: probed,
              },
              {
                feature: StorageFeature.ANONYMOUS_READ_POLICY,
                support: FeatureSupport.UNSUPPORTED,
                required: false,
                enables: "public collections",
                message:
                  "the store accepted the policy and refused the unsigned GET it allows",
                checkedAt: probed,
              },
            ],
          } as never
        }
      />,
    );
    const warnings = screen.getByRole("list", { name: "Backend warnings" });
    expect(
      within(warnings).getByText(
        /Not available on this backend: public collections/,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Conditional PUT")).toBeInTheDocument();
    expect(screen.getByText("Required")).toBeInTheDocument();
    expect(screen.getByText("Unsupported")).toBeInTheDocument();
    expect(
      screen.getByText(/refused the unsigned GET it allows/),
    ).toBeInTheDocument();
  });

  it("says so when every feature is supported", () => {
    render(
      <BackendFeatures
        backend={
          {
            compatibility: StorageCompatibility.COMPATIBLE,
            features: [
              {
                feature: StorageFeature.CONDITIONAL_PUT,
                support: FeatureSupport.SUPPORTED,
                required: true,
                enables: "x",
                message: "",
                checkedAt: probed,
              },
            ],
          } as never
        }
      />,
    );
    expect(
      screen.getByText("Every feature Paladin uses is supported."),
    ).toBeInTheDocument();
  });
});
