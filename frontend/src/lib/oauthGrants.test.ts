import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  CapabilityPrincipalSchema,
  CapabilitySchema,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import {
  capabilityGrant,
  grantCapabilitiesHref,
  grantHref,
  ownGrantHref,
} from "./oauthGrants";

const TENANT = "t-1";
const USER = "u-1";
const SESSION = "s-1";

function delegated(
  over: Partial<{ session: string; kind: PrincipalKind; tenant: string }> = {},
) {
  return create(CapabilitySchema, {
    session: over.session ?? SESSION,
    clientId: "agent",
    subject: create(CapabilityPrincipalSchema, {
      kind: PrincipalKind.AGENT,
      tenantId: TENANT,
      subject: "mcp",
    }),
    onBehalfOf: create(CapabilityPrincipalSchema, {
      kind: over.kind ?? PrincipalKind.USER,
      tenantId: over.tenant ?? TENANT,
      subject: USER,
    }),
  });
}

describe("oauthGrants", () => {
  it("opens a user's own grant on their applications page", () => {
    expect(ownGrantHref("g-1")).toBe("/applications?apps.id=g-1");
  });

  it("links a grant on its user's page, opened in the grants table", () => {
    expect(grantHref(TENANT, USER, SESSION)).toBe("/users/t-1/u-1?apps.id=s-1");
  });

  it("lists a grant's capabilities in its user's tenant", () => {
    expect(grantCapabilitiesHref(TENANT, SESSION)).toBe(
      "/tenants/t-1/capabilities?session=s-1",
    );
  });

  it("finds the grant a delegated capability was exchanged under", () => {
    expect(capabilityGrant(delegated())).toEqual({
      id: SESSION,
      userId: USER,
      tenantId: TENANT,
      clientId: "agent",
      href: "/users/t-1/u-1?apps.id=s-1",
    });
  });

  it("takes the capability's tenant when the user names none", () => {
    expect(capabilityGrant(delegated({ tenant: "" }))?.tenantId).toBe(TENANT);
  });

  it("finds none outside a grant, or for a principal that is no user", () => {
    expect(capabilityGrant(delegated({ session: "" }))).toBeNull();
    expect(
      capabilityGrant(delegated({ kind: PrincipalKind.SERVICE })),
    ).toBeNull();
    expect(
      capabilityGrant(create(CapabilitySchema, { session: SESSION })),
    ).toBeNull();
  });
});
