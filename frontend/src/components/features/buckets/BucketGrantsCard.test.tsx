import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({
  listBucketGrants: vi.fn(),
  grantBucket: vi.fn(),
  revokeBucketGrant: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: {
    listBucketGrants: h.listBucketGrants,
    grantBucket: h.grantBucket,
    revokeBucketGrant: h.revokeBucketGrant,
  },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import {
  BucketGrantsCard,
  GRANT_IN_USE_MESSAGE,
  GRANT_PICKER_LABEL,
} from "./BucketGrantsCard";
import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";

const BUCKET = "storageBackends/primary/buckets/pool";
const OWNER = "t-platform";
const TENANTS = [
  { tenantId: OWNER, slug: "platform", displayName: "Platform" },
  { tenantId: "t-acme", slug: "acme", displayName: "Acme" },
  { tenantId: "t-globex", slug: "globex", displayName: "Globex" },
];
const grant = (tenantId: string) => ({
  bucket: BUCKET,
  tenantId,
  grantedBy: "e2e-admin",
});

function renderCard(onChanged?: () => void) {
  render(
    <BucketGrantsCard
      bucketName={BUCKET}
      ownerTenantId={OWNER}
      tenants={TENANTS}
      onChanged={onChanged}
    />,
  );
}

beforeEach(() => {
  h.listBucketGrants.mockReset();
  h.grantBucket.mockReset();
  h.revokeBucketGrant.mockReset();
  h.showNotification.mockReset();
});

describe("BucketGrantsCard", () => {
  it("sorts the grantees by the name they show under, and searches the slug", async () => {
    h.listBucketGrants.mockResolvedValue({
      grants: [grant("t-globex"), grant("t-acme"), grant("t-gone")],
    });
    renderCard();
    await screen.findByRole("link", { name: /Acme/ });
    const grantees = () =>
      screen
        .getAllByRole("row")
        .slice(1)
        .map((r) => within(r).getAllByRole("cell")[0].textContent);
    await userEvent.click(screen.getByRole("button", { name: /^Tenant/ }));
    // A tenant the list does not know shows, and sorts, by its id.
    expect(grantees()).toEqual(["Acmeacme", "Globexglobex", "t-gone"]);
    await userEvent.type(
      screen.getByRole("searchbox", { name: "Search grants" }),
      "glob",
    );
    expect(grantees()).toEqual(["Globexglobex"]);
  });

  it("lists each grant by tenant name, with who granted it", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-acme")] });
    renderCard();
    const link = await screen.findByRole("link", { name: /Acme/ });
    expect(link).toHaveAttribute("href", "/tenants/acme");
    expect(screen.getByText("e2e-admin")).toBeInTheDocument();
    expect(h.listBucketGrants).toHaveBeenCalledWith({ name: BUCKET });
  });

  it("names an unresolvable grantee by its UUID", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-gone")] });
    renderCard();
    expect(await screen.findByText("t-gone")).toBeInTheDocument();
  });

  it("says an unshared bucket is bound by its owner only", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [] });
    renderCard();
    expect(
      await screen.findByText(/only the owner binds collections here/),
    ).toBeInTheDocument();
  });

  it("says the grant list failed rather than that there are none", async () => {
    h.listBucketGrants.mockRejectedValue(new Error("denied by policy"));
    renderCard();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Grants could not be loaded/,
    );
    expect(
      screen.queryByText(/only the owner binds collections here/),
    ).not.toBeInTheDocument();
  });

  // The owner binds without a grant, and a tenant holds at most one.
  it("offers neither the owner nor a granted tenant to grant", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-acme")] });
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderCard();
    await screen.findByRole("link", { name: /Acme/ });
    await user.click(
      screen.getByRole("combobox", { name: GRANT_PICKER_LABEL }),
    );
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["Globex (globex)"]);
  });

  it("grants the picked tenant", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [] });
    h.grantBucket.mockResolvedValue(grant("t-globex"));
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderCard();
    const grantButton = screen.getByRole("button", { name: "Grant" });
    expect(grantButton).toBeDisabled();
    await user.click(
      screen.getByRole("combobox", { name: GRANT_PICKER_LABEL }),
    );
    await user.click(
      await screen.findByRole("option", { name: "Globex (globex)" }),
    );
    await user.click(grantButton);
    await waitFor(() =>
      expect(h.grantBucket).toHaveBeenCalledWith({
        name: BUCKET,
        tenantId: "t-globex",
      }),
    );
    expect(
      await screen.findByRole("link", { name: /Globex/ }),
    ).toBeInTheDocument();
  });

  // The bucket's page counts its grantees elsewhere too; a grant tells it,
  // and a refused one does not.
  it.each([
    ["a grant that landed", true],
    ["a refused grant", false],
  ])("reports %s accordingly", async (_case, lands) => {
    h.listBucketGrants.mockResolvedValue({ grants: [] });
    if (lands) h.grantBucket.mockResolvedValue(grant("t-globex"));
    else h.grantBucket.mockRejectedValue(new Error("refused"));
    const onChanged = vi.fn();
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderCard(onChanged);
    await user.click(
      screen.getByRole("combobox", { name: GRANT_PICKER_LABEL }),
    );
    await user.click(
      await screen.findByRole("option", { name: "Globex (globex)" }),
    );
    await user.click(screen.getByRole("button", { name: "Grant" }));
    await waitFor(() => expect(h.grantBucket).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(lands ? 1 : 0));
  });

  it("revokes a grant only once confirmed, with a warning", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-acme")] });
    h.revokeBucketGrant.mockResolvedValue({});
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderCard();
    await user.click(
      await screen.findByRole("button", { name: "Revoke Acme (acme)" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByRole("note")).toHaveTextContent(
      /still has collections/,
    );
    expect(h.revokeBucketGrant).not.toHaveBeenCalled();
    await user.click(
      within(dialog).getByRole("button", { name: "Revoke grant" }),
    );
    await waitFor(() =>
      expect(h.revokeBucketGrant).toHaveBeenCalledWith({
        name: BUCKET,
        tenantId: "t-acme",
      }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: /Acme/ }),
      ).not.toBeInTheDocument(),
    );
  });

  // A revoke the server refuses while the tenant has collections in the
  // bucket is explained in words, and the grant stays listed.
  it("explains a revoke refused for the tenant's collections", async () => {
    h.listBucketGrants.mockResolvedValue({ grants: [grant("t-acme")] });
    h.revokeBucketGrant.mockRejectedValue(
      new ConnectError(
        "tenant has collections in the bucket",
        Code.FailedPrecondition,
        undefined,
        [
          {
            desc: ErrorInfoSchema,
            value: create(ErrorInfoSchema, {
              domain: "paladin",
              reason: "ERROR_REASON_BUCKET_GRANT_IN_USE",
            }),
          },
        ],
      ),
    );
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderCard();
    await user.click(
      await screen.findByRole("button", { name: "Revoke Acme (acme)" }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Revoke grant" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      GRANT_IN_USE_MESSAGE,
    );
    expect(screen.getByRole("link", { name: /Acme/ })).toBeInTheDocument();
  });
});
