import Link from "next/link";
import userEvent from "@testing-library/user-event";
import { render as plainRender } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  getTenant: vi.fn(),
  getBucket: vi.fn(),
  getUser: vi.fn(),
  get: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  tenantClient: { getTenant: h.getTenant },
  bucketClient: { getBucket: h.getBucket },
  userClient: { getUser: h.getUser },
  capabilityClient: { get: h.get },
  collectionClient: {},
}));

import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";

import {
  BucketHoverCard,
  CapabilityHoverCard,
  TenantHoverCard,
  UserHoverCard,
} from "./ResourceHoverCards";

beforeEach(() => {
  for (const fn of Object.values(h)) fn.mockReset();
});

/** Rests the pointer on the trigger until the card opens. */
async function hover(name: string) {
  await userEvent.hover(screen.getByRole("link", { name }));
  return screen.findByLabelText(/profile$/, {}, { timeout: 2_000 });
}

describe("TenantHoverCard", () => {
  const trigger = (
    <TenantHoverCard handle="acme">
      <Link href="/tenants/acme">acme</Link>
    </TenantHoverCard>
  );

  it("reads nothing until the pointer rests on it", () => {
    render(trigger);
    expect(h.getTenant).not.toHaveBeenCalled();
  });

  it("profiles the tenant once opened, with a way to its page", async () => {
    h.getTenant.mockResolvedValue({
      tenantId: "t-1",
      slug: "acme",
      displayName: "Acme Corp",
      storageLayout: "dedicated",
      deletedAt: { seconds: 1n, nanos: 0 },
    });
    render(trigger);
    const card = await hover("acme");
    await waitFor(() => expect(card).toHaveTextContent("Acme Corp"));
    expect(card).toHaveTextContent("dedicated storage");
    expect(card).toHaveTextContent("in trash");
    expect(h.getTenant.mock.calls[0][0]).toEqual({ name: "tenants/acme" });
    expect(screen.getByRole("link", { name: "Open tenant" })).toHaveAttribute(
      "href",
      "/tenants/acme",
    );
  });

  it("says the tenant could not be read, and why", async () => {
    h.getTenant.mockRejectedValue(new Error("permission denied"));
    render(trigger);
    const card = await hover("acme");
    await waitFor(() =>
      expect(card).toHaveTextContent(
        /Tenant could not be read: .*permission denied/,
      ),
    );
  });

  // A table rendered alone, as in a page's own test, has no client to read with.
  it("shows only its trigger without a query client", () => {
    plainRender(trigger);
    expect(screen.getByRole("link", { name: "acme" })).toBeInTheDocument();
  });
});

describe("BucketHoverCard", () => {
  it("profiles a bucket: access, owner, sharing", async () => {
    h.getBucket.mockResolvedValue({
      backendId: "minio",
      bucketId: "logs",
      displayName: "",
      publicRead: true,
      provisionState: "ready",
      ownerTenantId: "t-1",
      grantedTenantIds: ["t-2", "t-3"],
      region: "eu",
    });
    render(
      <BucketHoverCard backendId="minio" bucketId="logs">
        <Link href="/b">logs</Link>
      </BucketHoverCard>,
    );
    const card = await hover("logs");
    await waitFor(() => expect(card).toHaveTextContent("minio/logs"));
    expect(card).toHaveTextContent("public");
    expect(card).toHaveTextContent("t-1");
    expect(card).toHaveTextContent("2 tenants");
    expect(h.getBucket.mock.calls[0][0]).toEqual({
      name: "storageBackends/minio/buckets/logs",
    });
  });
});

describe("UserHoverCard", () => {
  it("profiles a user: state, roles, last login", async () => {
    h.getUser.mockResolvedValue({
      userId: "u-1",
      tenantId: "t-1",
      subject: "ada",
      displayName: "Ada",
      disabled: true,
      roles: ["tenant.admin"],
    });
    render(
      <UserHoverCard name="tenants/t-1/users/u-1">
        <Link href="/u">Ada</Link>
      </UserHoverCard>,
    );
    const card = await hover("Ada");
    await waitFor(() => expect(card).toHaveTextContent("disabled"));
    expect(card).toHaveTextContent("tenant.admin");
    expect(screen.getByRole("link", { name: "Open user" })).toHaveAttribute(
      "href",
      "/users/t-1/u-1",
    );
  });
});

describe("CapabilityHoverCard", () => {
  it("profiles a capability: who holds it, whether it is revoked", async () => {
    h.get.mockResolvedValue({
      capability: {
        id: "cap-1",
        subject: {
          kind: PrincipalKind.AGENT,
          subject: "agent-x",
          tenantId: "t-1",
        },
        audience: ["data"],
      },
      revocation: { reason: "leaked" },
    });
    render(
      <CapabilityHoverCard id="cap-1" tenantId="t-1">
        <Link href="/c">cap-1</Link>
      </CapabilityHoverCard>,
    );
    const card = await hover("cap-1");
    await waitFor(() => expect(card).toHaveTextContent("agent agent-x"));
    expect(card).toHaveTextContent("revoked");
    expect(card).not.toHaveTextContent("not revoked");
    expect(card).toHaveTextContent("never");
    expect(
      screen.getByRole("link", { name: "Open capability" }),
    ).toHaveAttribute("href", "/tenants/t-1/capabilities?id=cap-1");
  });
});
