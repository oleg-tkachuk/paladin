import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({
  pathname: "/storage-backends/primary/buckets/pool/lifecycle",
  grantees: [] as string[],
}));
vi.mock("next/navigation", () => ({ usePathname: () => h.pathname }));
vi.mock("./bucket-context", () => ({
  useBucket: () => ({
    bucket: { grantedTenantIds: h.grantees },
    owner: {
      tenantId: "t-o",
      slug: "owner",
      displayName: "Owner Co",
      trashed: false,
    },
  }),
}));

import {
  SharedBucketNotice,
  sharedSettingsMessage,
} from "./SharedBucketNotice";

describe("SharedBucketNotice", () => {
  beforeEach(() => {
    h.pathname = "/storage-backends/primary/buckets/pool/lifecycle";
    h.grantees = ["t-a", "t-b"];
  });

  it("says a setting reaches the tenants the bucket is shared with", () => {
    render(<SharedBucketNotice />);
    expect(screen.getByRole("note")).toHaveTextContent(
      sharedSettingsMessage(2, "Owner Co"),
    );
  });

  it("says nothing for a bucket shared with nobody", () => {
    h.grantees = [];
    render(<SharedBucketNotice />);
    expect(screen.queryByRole("note")).toBeNull();
  });

  // The overview and the collections change nothing for the grantees.
  it.each(["", "/collections"])("says nothing on %o", (tab) => {
    h.pathname = `/storage-backends/primary/buckets/pool${tab}`;
    render(<SharedBucketNotice />);
    expect(screen.queryByRole("note")).toBeNull();
  });
});

describe("sharedSettingsMessage", () => {
  it("counts one tenant and many", () => {
    expect(sharedSettingsMessage(1, "X")).toContain("1 other tenant:");
    expect(sharedSettingsMessage(3, "X")).toContain("3 other tenants:");
  });
});
