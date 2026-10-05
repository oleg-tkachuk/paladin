import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { PolicyLayers } from "./PolicyLayers";
import type { PolicyLayer } from "@/gen/paladin/admin/v1/policy_service_pb";

const layer = (over: Partial<PolicyLayer>) =>
  ({
    source: "tenants/t-1",
    cedarPolicy: "permit(principal, action, resource);",
    frozen: false,
    evaluatedCedarPolicy: "permit(principal, action, resource);",
    ...over,
  }) as PolicyLayer;

// A layer that does not compile is not what decides a request: the freeze in
// its place is. Showing only the stored text would have an operator reading
// rules nothing evaluates.
describe("PolicyLayers", () => {
  it("shows each layer under its source", () => {
    render(
      <PolicyLayers layers={[layer({ source: "built-in" }), layer({})]} />,
    );
    expect(screen.getByText("built-in")).toBeInTheDocument();
    expect(screen.getByText("tenants/t-1")).toBeInTheDocument();
    expect(screen.queryByText(/Frozen/)).not.toBeInTheDocument();
  });

  it("marks a frozen layer and shows what is evaluated in its place", () => {
    render(
      <PolicyLayers
        layers={[
          layer({
            frozen: true,
            cedarPolicy: "not cedar",
            evaluatedCedarPolicy: "forbid(principal, action, resource);",
          }),
        ]}
      />,
    );
    expect(screen.getByText(/Frozen/)).toBeInTheDocument();
    expect(
      screen.getByText("forbid(principal, action, resource);"),
    ).toBeInTheDocument();
    expect(screen.getByText("not cedar")).toBeInTheDocument();
  });
});
