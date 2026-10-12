import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";

import {
  FeatureSupport,
  StorageCompatibility,
  StorageFeature,
} from "@/gen/paladin/admin/v1/types_pb";
import { formatDateTime } from "@/lib/format/locale";
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
    // Required features and optional ones are listed apart.
    const required = screen.getByRole("region", {
      name: "Required by Paladin",
    });
    expect(within(required).getByText("Conditional PUT")).toBeInTheDocument();
    expect(within(required).getByText("Supported")).toBeInTheDocument();
    const optional = screen.getByRole("region", { name: "Optional" });
    expect(
      within(optional).getByText("Anonymous read policy"),
    ).toBeInTheDocument();
    expect(within(optional).getByText("Unsupported")).toBeInTheDocument();
    expect(
      within(optional).getByText("Needed for public collections"),
    ).toBeInTheDocument();
    // What the store answered is there, folded under the row.
    const output = within(optional).getByText(
      /refused the unsigned GET it allows/,
    );
    expect(output.closest("details")).not.toHaveAttribute("open");
  });

  it("folds no probe output under a supported feature", () => {
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
                message: "answered 412 to an If-None-Match PUT",
                checkedAt: probed,
              },
            ],
          } as never
        }
      />,
    );
    expect(screen.queryByText("Probe output")).toBeNull();
    expect(screen.queryByText(/answered 412/)).toBeNull();
  });

  it("summarises the probe in the header: count, when, and compatibility", () => {
    const later = { seconds: BigInt(1_800_000_000), nanos: 0 };
    render(
      <BackendFeatures
        backend={
          {
            compatibility: StorageCompatibility.UNVERIFIED,
            features: [
              {
                feature: StorageFeature.CONDITIONAL_PUT,
                support: FeatureSupport.SUPPORTED,
                required: true,
                enables: "x",
                message: "",
                checkedAt: probed,
              },
              {
                feature: StorageFeature.MULTIPART_UPLOAD,
                support: FeatureSupport.UNKNOWN,
                required: true,
                enables: "y",
                message: "StatusCode: 500",
                checkedAt: later,
              },
            ],
          } as never
        }
      />,
    );
    expect(screen.getByText("1 of 2 supported")).toBeInTheDocument();
    expect(
      screen.getByText(
        `Probed ${formatDateTime(new Date(Number(later.seconds) * 1000))}`,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Unverified")).toBeInTheDocument();
  });

  it("says a backend was never probed", () => {
    render(
      <BackendFeatures
        backend={
          {
            compatibility: StorageCompatibility.UNVERIFIED,
            features: [
              {
                feature: StorageFeature.CONDITIONAL_PUT,
                support: FeatureSupport.UNKNOWN,
                required: true,
                enables: "x",
                message: "",
              },
            ],
          } as never
        }
      />,
    );
    expect(screen.getByText("Never probed")).toBeInTheDocument();
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
