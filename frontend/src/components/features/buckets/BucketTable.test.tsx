import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";

import { BucketSchema } from "@/gen/paladin/admin/v1/types_pb";

import {
  BucketTable,
  GRANTEES_NAMED,
  NOT_SHARED_LABEL,
  spansBackends,
  tenantLabel,
} from "./BucketTable";

const OWNER = "11111111-1111-1111-1111-111111111111";
const A = "22222222-2222-2222-2222-222222222222";
const B = "33333333-3333-3333-3333-333333333333";
const C = "44444444-4444-4444-4444-444444444444";

const tenants = new Map([
  [OWNER, { slug: "platform", displayName: "Platform" }],
  [A, { slug: "tenant-a", displayName: "" }],
  [B, { slug: "tenant-b", displayName: "Tenant B" }],
]);

const bucket = (over: Partial<Parameters<typeof create>[1]> = {}) =>
  create(BucketSchema, {
    backendId: "primary",
    bucketId: "b1",
    ownerTenantId: OWNER,
    provisionState: "ready",
    ...over,
  });

function table(buckets: ReturnType<typeof bucket>[], extra = {}) {
  render(
    <BucketTable
      buckets={buckets}
      tenants={tenants}
      loading={false}
      error={null}
      onRetry={vi.fn()}
      empty="none here"
      {...extra}
    />,
  );
}

describe("BucketTable", () => {
  it("names the owner and links to the bucket's one page", () => {
    table([bucket()]);
    expect(
      screen.getByRole("link", { name: "Platform" }).getAttribute("href"),
    ).toBe(`/tenants/${OWNER}`);
    expect(screen.getByRole("link", { name: /b1/ }).getAttribute("href")).toBe(
      "/storage-backends/primary/buckets/b1",
    );
  });

  it("says who a bucket is shared with, and counts past the first few", () => {
    table([bucket({ grantedTenantIds: [A, B, C] })]);
    const cell = screen.getByText(/tenant-a, Tenant B/);
    expect(cell.textContent).toContain(`+${3 - GRANTEES_NAMED}`);
  });

  it("says a bucket nobody was granted is the owner's only", () => {
    table([bucket()]);
    expect(screen.getByText(NOT_SHARED_LABEL)).toBeTruthy();
  });

  // One backend in the rows: a column saying it on every row says nothing.
  it("shows the backend column only when the rows span backends", () => {
    table([bucket()]);
    expect(screen.queryByRole("columnheader", { name: "Backend" })).toBeNull();
  });

  it("shows the backend column when they do", () => {
    table([bucket(), bucket({ backendId: "secondary", bucketId: "b2" })]);
    expect(screen.getByRole("columnheader", { name: "Backend" })).toBeTruthy();
  });

  it("marks owned and granted rows on a tenant's list", () => {
    table([bucket({ ownerTenantId: A, bucketId: "mine" }), bucket()], {
      perspective: A,
    });
    const rows = screen.getAllByRole("row").slice(1);
    expect(within(rows[0]).getByText("owned")).toBeTruthy();
    expect(within(rows[1]).getByText("granted")).toBeTruthy();
  });

  it("says the list failed rather than that it is empty", () => {
    table([], { error: "boom" });
    expect(screen.queryByText("none here")).toBeNull();
    expect(screen.getByText(/boom/)).toBeTruthy();
  });

  it("shows its empty state when there are no rows", () => {
    table([]);
    expect(screen.getByText("none here")).toBeTruthy();
  });
});

describe("tenantLabel", () => {
  it("prefers the display name, then the slug, then the id", () => {
    expect(tenantLabel(tenants, B)).toBe("Tenant B");
    expect(tenantLabel(tenants, A)).toBe("tenant-a");
    expect(tenantLabel(tenants, C)).toBe(C);
  });
});

describe("spansBackends", () => {
  it("is true only for rows on more than one backend", () => {
    expect(spansBackends([bucket(), bucket({ bucketId: "b2" })])).toBe(false);
    expect(spansBackends([bucket(), bucket({ backendId: "x" })])).toBe(true);
  });
});
