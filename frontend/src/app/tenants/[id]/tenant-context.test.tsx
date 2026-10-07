import { describe, expect, it } from "vitest";
import { render, screen } from "@/test/utils";

import {
  TenantProvider,
  TRASHED_TENANT_BLOCK,
  useTenantChangesBlocked,
} from "./tenant-context";

function Probe() {
  return <p>{useTenantChangesBlocked() ?? "open"}</p>;
}

function renderFor(trashed: boolean) {
  render(
    <TenantProvider
      value={{
        tenantId: "t-1",
        slug: "acme",
        displayName: "Acme",
        storageLayout: "shared",
        trashed,
      }}
    >
      <Probe />
    </TenantProvider>,
  );
}

// The server refuses every change to a tenant in the trash, so the console
// holds the controls and says why.
describe("useTenantChangesBlocked", () => {
  it("blocks changes to a trashed tenant", () => {
    renderFor(true);
    expect(screen.getByText(TRASHED_TENANT_BLOCK)).toBeInTheDocument();
  });

  it("leaves a live tenant open", () => {
    renderFor(false);
    expect(screen.getByText("open")).toBeInTheDocument();
  });

  it("leaves a page outside a tenant open", () => {
    render(<Probe />);
    expect(screen.getByText("open")).toBeInTheDocument();
  });
});
