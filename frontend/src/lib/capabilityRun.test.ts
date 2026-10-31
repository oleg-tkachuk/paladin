import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  CapabilitySchema,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import { capabilityRun, runCapabilitiesHref } from "./capabilityRun";

const cap = (kind: PrincipalKind, runId: string) =>
  create(CapabilitySchema, { subject: { kind, subject: "a", runId } });

describe("capabilityRun", () => {
  it("is an agent capability's run", () => {
    expect(capabilityRun(cap(PrincipalKind.AGENT, "r-1"))).toBe("r-1");
  });

  it("is none for an agent with no run, or a principal that is no agent", () => {
    expect(capabilityRun(cap(PrincipalKind.AGENT, ""))).toBeNull();
    expect(capabilityRun(cap(PrincipalKind.USER, "r-1"))).toBeNull();
    expect(capabilityRun(create(CapabilitySchema, {}))).toBeNull();
  });

  it("links to the run's capabilities on its tenant's page", () => {
    expect(runCapabilitiesHref("acme", "r-1")).toBe(
      "/tenants/acme/capabilities?run=r-1",
    );
  });
});
