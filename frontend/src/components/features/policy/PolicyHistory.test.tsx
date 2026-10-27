import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError } from "@connectrpc/connect";

import { render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({ listPolicyVersions: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  policyClient: { listPolicyVersions: h.listPolicyVersions },
}));
// The writer's name is ActorName's to resolve; here it shows the subject.
vi.mock("@/components/features/audit/ActorName", () => ({
  ActorName: ({ subject, tenantId }: { subject: string; tenantId: string }) => (
    <span data-tenant={tenantId}>{subject}</span>
  ),
}));

import { PolicyHistory } from "./PolicyHistory";

const AT = { seconds: 1_800_000_000n, nanos: 0 };
const LAYER = "tenants/acme";

function version(
  n: number,
  cedarPolicy: string,
  writtenBy: string | undefined = "agent:ci",
) {
  return { version: n, cedarPolicy, writtenBy, writtenAt: AT };
}

function answer(versions: unknown[], nextPageToken = "") {
  return { versions, page: { nextPageToken } };
}

beforeEach(() => {
  h.listPolicyVersions.mockReset();
});

describe("PolicyHistory", () => {
  it("lists the versions with their writers, and diffs the newest against the one before", async () => {
    h.listPolicyVersions.mockResolvedValue(
      answer([version(2, "permit-two"), version(1, "permit-one", "u-1")]),
    );
    render(<PolicyHistory resourceName={LAYER} />);
    const table = await screen.findByRole("table", { name: "Policy versions" });
    const rows = within(table).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("v2");
    expect(rows[1]).toHaveTextContent("agent:ci");
    expect(rows[2]).toHaveTextContent("u-1");
    // The writer is looked up in the layer's tenant.
    expect(within(rows[2]).getByText("u-1")).toHaveAttribute(
      "data-tenant",
      "acme",
    );
    expect(h.listPolicyVersions).toHaveBeenCalledWith(
      { resourceName: LAYER, page: { pageSize: 20, pageToken: "" } },
      expect.anything(),
    );
    const diff = screen.getByRole("table", { name: "Changes from v1 to v2" });
    expect(diff).toHaveTextContent("permit-one");
    expect(diff).toHaveTextContent("permit-two");
  });

  it("looks a writer up in the tenant the server recorded for it", async () => {
    const PLATFORM = "22222222-2222-4222-8222-222222222222";
    h.listPolicyVersions.mockResolvedValue(
      answer([
        { ...version(2, "two", "admin-id"), writtenByTenantId: PLATFORM },
      ]),
    );
    render(<PolicyHistory resourceName={LAYER} />);
    const table = await screen.findByRole("table", { name: "Policy versions" });
    // A platform admin edited acme's policy: its user record is the platform's.
    expect(within(table).getByText("admin-id")).toHaveAttribute(
      "data-tenant",
      PLATFORM,
    );
  });

  it("diffs the two versions the user picks", async () => {
    h.listPolicyVersions.mockResolvedValue(
      answer([version(3, "three"), version(2, "two"), version(1, "one")]),
    );
    const user = userEvent.setup();
    render(<PolicyHistory resourceName={LAYER} />);
    await user.click(
      await screen.findByRole("radio", { name: /^Compare from v1 ·/ }),
    );
    await user.click(screen.getByRole("radio", { name: /^Compare to v2 ·/ }));
    const diff = screen.getByRole("table", { name: "Changes from v1 to v2" });
    expect(diff).toHaveTextContent("one");
    expect(diff).toHaveTextContent("two");
    expect(diff).not.toHaveTextContent("three");
  });

  it("compares a version with the editor's draft", async () => {
    h.listPolicyVersions.mockResolvedValue(
      answer([version(2, "two"), version(1, "one")]),
    );
    const user = userEvent.setup();
    render(<PolicyHistory resourceName={LAYER} draft="drafted" />);
    await user.click(
      await screen.findByRole("radio", { name: "Compare to Draft" }),
    );
    const diff = screen.getByRole("table", {
      name: "Changes from v1 to draft",
    });
    expect(diff).toHaveTextContent("drafted");
  });

  it("marks a version that cleared the policy, and diffs it as empty", async () => {
    h.listPolicyVersions.mockResolvedValue(
      answer([version(2, ""), version(1, "permit-one")]),
    );
    render(<PolicyHistory resourceName={LAYER} />);
    const table = await screen.findByRole("table", { name: "Policy versions" });
    expect(within(table).getAllByRole("row")[1]).toHaveTextContent("(cleared)");
    const diff = screen.getByRole("table", { name: "Changes from v1 to v2" });
    expect(diff.querySelector('[data-diff="remove"]')).not.toBeNull();
    expect(diff.querySelector('[data-diff="add"]')).toBeNull();
  });

  it("says when no request wrote a version", async () => {
    h.listPolicyVersions.mockResolvedValue(
      answer([{ ...version(1, "seeded"), writtenBy: undefined }]),
    );
    render(<PolicyHistory resourceName="storageBackends/minio" />);
    const table = await screen.findByRole("table", { name: "Policy versions" });
    expect(table).toHaveTextContent("not recorded");
    expect(screen.getByText("No earlier versions.")).toBeInTheDocument();
    // One version and no draft: nothing to compare.
    expect(screen.queryByRole("table", { name: /^Changes from/ })).toBeNull();
  });

  it("says a layer that never held a policy has no earlier versions", async () => {
    h.listPolicyVersions.mockResolvedValue(answer([]));
    render(<PolicyHistory resourceName={LAYER} draft="" />);
    expect(await screen.findByText("No earlier versions.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("reads older versions on demand", async () => {
    h.listPolicyVersions
      .mockResolvedValueOnce(answer([version(3, "three")], "next"))
      .mockResolvedValueOnce(answer([version(2, "two")]));
    const user = userEvent.setup();
    render(<PolicyHistory resourceName={LAYER} />);
    await user.click(await screen.findByRole("button", { name: "Load older" }));
    expect(
      await screen.findByRole("radio", { name: /^Compare from v2 ·/ }),
    ).toBeInTheDocument();
    expect(h.listPolicyVersions).toHaveBeenLastCalledWith(
      { resourceName: LAYER, page: { pageSize: 20, pageToken: "next" } },
      expect.anything(),
    );
    expect(screen.queryByRole("button", { name: "Load older" })).toBeNull();
    expect(
      screen.getByRole("table", { name: "Changes from v2 to v3" }),
    ).toBeInTheDocument();
  });

  it("says the history could not be read, and reads it again on retry", async () => {
    h.listPolicyVersions
      .mockRejectedValueOnce(new ConnectError("down", Code.Unavailable))
      .mockResolvedValueOnce(answer([version(1, "one")]));
    const user = userEvent.setup();
    render(<PolicyHistory resourceName={LAYER} />);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/The policy history could not be loaded/);
    expect(alert).toHaveTextContent("down");
    await user.click(within(alert).getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("table", { name: "Policy versions" }),
    ).toBeInTheDocument();
  });

  it("says a layer that does not exist has no history, not that it failed", async () => {
    h.listPolicyVersions.mockRejectedValue(
      new ConnectError("no such collection", Code.NotFound),
    );
    render(<PolicyHistory resourceName="tenants/acme/collections/gone" />);
    expect(
      await screen.findByText(/This layer has no history to show/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("reads nothing for no layer", () => {
    render(<PolicyHistory resourceName="" />);
    expect(h.listPolicyVersions).not.toHaveBeenCalled();
  });
});
