import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({
  get: vi.fn(),
  getUsage: vi.fn(),
  listOperationGrants: vi.fn(),
  getToken: vi.fn(),
  revoke: vi.fn(),
  revokeRun: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: {
    get: h.get,
    getUsage: h.getUsage,
    listOperationGrants: h.listOperationGrants,
  },
  apiTokenClient: { get: h.getToken },
}));
vi.mock("@/lib/utils", async (orig) => ({
  ...(await orig<typeof import("@/lib/utils")>()),
  copyToClipboard: vi.fn(async () => true),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import {
  CapabilityGrantBasis,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { CapabilityDetailsPanel } from "./CapabilityDetailsPanel";

const CAP = {
  id: "cap-1",
  issuer: "paladin",
  audience: ["data"],
  parentId: "",
  subject: { kind: PrincipalKind.AGENT, subject: "agent:1", tenantId: "t-1" },
  caveats: {
    ops: ["get"],
    requestCeiling: { case: "maxRequests", value: 0n },
    copyCeiling: { case: "maxCopies", value: 0n },
    budgetCeiling: {
      case: "unlimitedBudget",
      value: { currencyCode: "USD" },
    },
  },
} as unknown as Capability;

const NO_FIELDS = { actions: [], pairedOps: [], procedures: [] };
// The server's table, as ListOperationGrants serves it, one row per basis.
const GRANTS = [
  {
    ...NO_FIELDS,
    op: "get",
    basis: CapabilityGrantBasis.BUILTIN,
    actions: ["GetObject", "PresignGet"],
  },
  { ...NO_FIELDS, op: "manage", basis: CapabilityGrantBasis.TENANT_POLICY },
  {
    ...NO_FIELDS,
    op: "object:presign",
    basis: CapabilityGrantBasis.PAIRED,
    pairedOps: ["get", "put"],
  },
  {
    ...NO_FIELDS,
    op: "share",
    basis: CapabilityGrantBasis.CAVEATS_ONLY,
    procedures: ["paladin.admin.v1.CapabilityService.Delegate"],
  },
];
const UNLOCKS = "What these ops unlock";

function open(cap: Capability = CAP) {
  render(
    <CapabilityDetailsPanel
      id={cap.id}
      cap={cap}
      usageEntry="never"
      onClose={vi.fn()}
      onRevoke={h.revoke}
      tenant="acme"
      onRevokeRun={h.revokeRun}
    />,
  );
}

describe("CapabilityDetailsPanel", () => {
  beforeEach(() => {
    h.get.mockReset();
    h.getUsage.mockReset();
    h.listOperationGrants.mockReset();
    h.listOperationGrants.mockResolvedValue({ grants: GRANTS });
  });

  describe("what its ops unlock", () => {
    it("lists each op's actions beside the allowed ops", async () => {
      h.get.mockResolvedValue({ capability: CAP });
      open();
      const unlocks = await screen.findByRole("group", { name: UNLOCKS });
      expect(within(unlocks).getByText("GetObject")).toBeInTheDocument();
      expect(within(unlocks).getByText("PresignGet")).toBeInTheDocument();
      expect(screen.queryByRole("note")).not.toBeInTheDocument();
    });

    // A manage capability fails with "denied by policy" on a tenant with no
    // policy for it; the panel says why before anyone reads the audit log.
    it("warns that manage needs a tenant policy", async () => {
      const cap = {
        ...CAP,
        caveats: { ...CAP.caveats, ops: ["get", "manage"] },
      } as unknown as Capability;
      h.get.mockResolvedValue({ capability: cap });
      open(cap);
      expect(await screen.findByRole("note")).toHaveTextContent(
        "manage unlocks nothing on its own: without a tenant policy that permits its actions, each call is denied by policy.",
      );
    });

    it("says object:presign adds a check beside get or put", async () => {
      const cap = {
        ...CAP,
        caveats: { ...CAP.caveats, ops: ["get", "object:presign"] },
      } as unknown as Capability;
      h.get.mockResolvedValue({ capability: cap });
      open(cap);
      const unlocks = await screen.findByRole("group", { name: UNLOCKS });
      expect(
        within(unlocks).getByText(/adds a check beside/),
      ).toHaveTextContent(
        "object:presign adds a check beside get or put; it unlocks nothing on its own.",
      );
      expect(screen.queryByRole("note")).not.toBeInTheDocument();
    });

    it("says share is decided by the caveats alone", async () => {
      const cap = {
        ...CAP,
        caveats: { ...CAP.caveats, ops: ["share"] },
      } as unknown as Capability;
      h.get.mockResolvedValue({ capability: cap });
      open(cap);
      const unlocks = await screen.findByRole("group", { name: UNLOCKS });
      expect(
        within(unlocks).getByText(/is decided by the caveats alone/),
      ).toHaveTextContent(
        "share is decided by the caveats alone: it opens CapabilityService.Delegate.",
      );
      expect(screen.queryByRole("note")).not.toBeInTheDocument();
    });

    // A row with no basis the console knows is not a grant: no badges.
    it("counts an op with an unspecified basis as not granted", async () => {
      h.listOperationGrants.mockResolvedValue({
        grants: [
          {
            ...NO_FIELDS,
            op: "get",
            basis: CapabilityGrantBasis.UNSPECIFIED,
            actions: ["GetObject"],
          },
        ],
      });
      h.get.mockResolvedValue({ capability: CAP });
      open();
      const unlocks = await screen.findByRole("group", { name: UNLOCKS });
      expect(within(unlocks).queryByText("GetObject")).not.toBeInTheDocument();
      expect(unlocks).toHaveTextContent(
        "The server's table does not say what get unlocks; it is not counted as granted.",
      );
    });

    it("says the table is unavailable when the read fails", async () => {
      h.get.mockResolvedValue({ capability: CAP });
      h.listOperationGrants.mockRejectedValue(new Error("down"));
      open();
      expect(
        await screen.findByText("What these ops unlock is unavailable."),
      ).toBeInTheDocument();
      // The rest of the panel reads on.
      expect(screen.getByText("Allowed ops")).toBeInTheDocument();
    });

    it("has no row for a capability with no ops", async () => {
      const cap = {
        ...CAP,
        caveats: { ...CAP.caveats, ops: [] },
      } as unknown as Capability;
      h.get.mockResolvedValue({ capability: cap });
      open(cap);
      expect(await screen.findByText("Allowed ops")).toBeInTheDocument();
      expect(screen.queryByText("Unlocks")).not.toBeInTheDocument();
      expect(h.listOperationGrants).not.toHaveBeenCalled();
    });
  });

  it("reads the record for its issuer and revocation", async () => {
    h.get.mockResolvedValue({
      capability: CAP,
      issuedBy: { kind: PrincipalKind.USER, subject: "operator@example.com" },
      revocation: {
        revokedAt: timestampFromDate(new Date("2026-10-08T12:00:00Z")),
        reason: "leaked in a log",
        actor: "security",
        cascade: true,
      },
    });
    open();
    expect(
      await screen.findByText(/operator@example\.com/),
    ).toBeInTheDocument();
    expect(screen.getByText("leaked in a log")).toBeInTheDocument();
    expect(screen.getByText("security")).toBeInTheDocument();
    expect(
      screen.getByText(/with everything delegated from it/),
    ).toBeInTheDocument();
    expect(h.get).toHaveBeenCalledWith({ id: "cap-1" }, expect.anything());
  });

  // An API token is named as the console names it everywhere else, not by
  // the bare apikey:<id> it signs as.
  it("names an API token that issued or revoked the capability", async () => {
    const tenant = "01a0fcfc-281e-7c59-963c-8a946fe82a47";
    const actor = "apikey:c96e0f84-b32b-405d-81af-2a091f0faea6";
    h.getToken.mockResolvedValue({ apiToken: { displayName: "rag-api" } });
    const cap = {
      ...CAP,
      subject: { ...CAP.subject, tenantId: tenant },
    } as unknown as Capability;
    h.get.mockResolvedValue({
      capability: cap,
      issuedBy: { kind: PrincipalKind.USER, subject: actor, tenantId: tenant },
      revocation: { actor, reason: "run finished" },
    });
    render(
      <CapabilityDetailsPanel
        id={cap.id}
        cap={cap}
        usageEntry="never"
        onClose={vi.fn()}
        onRevoke={h.revoke}
        tenant="acme"
        onRevokeRun={h.revokeRun}
      />,
    );
    await waitFor(() => expect(screen.getAllByText("rag-api")).toHaveLength(2));
    expect(screen.getAllByTitle(actor)).toHaveLength(2);
  });

  // The id is the panel's header; a row repeating it only pushed the rest
  // down, and the status leads.
  it("leads with the lifetime, the id only in the header", async () => {
    h.get.mockResolvedValue({ capability: CAP });
    open();
    await screen.findByRole("heading", { name: "Lifetime" });
    expect(screen.queryByText("Capability ID")).toBeNull();
    const sections = [...document.querySelectorAll("h3")].map(
      (e) => e.textContent,
    );
    expect(sections[0]).toBe("Lifetime");
  });

  // Opened by a link, the capability comes from Get, which names no
  // disposition: its own revocation says it is revoked, and there is nothing
  // left to revoke.
  it("reads a revoked capability as revoked, and offers no revoke", async () => {
    h.get.mockResolvedValue({
      capability: CAP,
      revocation: { actor: "security", reason: "leaked" },
    });
    open();
    expect(await screen.findByText("revoked")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Revoke/ })).toBeNull();
  });

  it("offers a revoke while the capability is not revoked", async () => {
    h.get.mockResolvedValue({ capability: CAP });
    open();
    expect(await screen.findByText("not revoked itself")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Revoke/ })).toBeInTheDocument();
  });

  // Stopped only through an ancestor: no entry of its own, so the panel
  // names the ancestor whose entry stopped it, and links to it.
  it("names the ancestor whose revocation stopped it", async () => {
    const ancestor = "01a15ec0-a510-7a88-8ec4-1733c36e17a4";
    h.get.mockResolvedValue({
      capability: CAP,
      stoppedBy: {
        capabilityId: ancestor,
        revocation: {
          reason: "run finished",
          revokedAt: timestampFromDate(new Date("2026-10-21T13:59:57Z")),
        },
      },
    });
    open();
    const link = await screen.findByRole("link", { name: /01a15ec0/ });
    expect(link).toHaveAttribute("href", `?id=${ancestor}`);
    expect(link).toHaveAttribute("title", ancestor);
    expect(screen.getByText("run finished")).toBeInTheDocument();
    expect(screen.queryByText("not revoked itself")).toBeNull();
  });

  it("says when the capability has no revocation of its own", async () => {
    h.get.mockResolvedValue({
      capability: CAP,
      issuedBy: { subject: "operator" },
    });
    open();
    expect(await screen.findByText("not revoked itself")).toBeInTheDocument();
  });

  // 0 is a limit that allows nothing, distinct from no limit at all.
  it("tells a limit of zero from an unlimited ceiling", async () => {
    h.get.mockResolvedValue({ capability: CAP });
    open();
    // Each label's <dd> is the element after its <dt>.
    const value = (label: string) =>
      screen.getByText(label).nextElementSibling?.textContent;
    expect(value("Max requests")).toBe("0");
    expect(value("Max Biscuit copies")).toBe("0");
    expect(value("Max budget")).toBe("unlimited");
  });

  // Its lifetime runs from when it may first be used: notBefore when the
  // issuer delayed it, else the moment it was issued.
  describe("validity span", () => {
    const ISSUED_S = 1_700_000_000;
    const HOUR_S = 3_600;
    const DAY_S = 24 * HOUR_S;
    const NOT_BEFORE_DELAY_S = 2 * HOUR_S;
    const LIFETIME_S = 7 * DAY_S;
    const at = (seconds: number) => ({ seconds: BigInt(seconds), nanos: 0 });

    it.each([
      ["from issue with no notBefore", undefined, "7 d"],
      [
        "from notBefore over issue",
        at(ISSUED_S + NOT_BEFORE_DELAY_S),
        "6 d 22 h",
      ],
    ])("is measured %s", async (_, notBefore, want) => {
      const cap = {
        ...CAP,
        issuedAt: at(ISSUED_S),
        notBefore,
        expiresAt: at(ISSUED_S + LIFETIME_S),
      } as unknown as Capability;
      h.get.mockResolvedValue({ capability: cap });
      render(
        <CapabilityDetailsPanel
          id={cap.id}
          cap={cap}
          usageEntry="never"
          onClose={vi.fn()}
          onRevoke={h.revoke}
          tenant="acme"
          onRevokeRun={h.revokeRun}
        />,
      );
      await waitFor(() =>
        expect(
          screen.getByText("Valid for").nextElementSibling?.textContent,
        ).toBe(want),
      );
    });
  });

  it("marks the record unavailable when the read fails", async () => {
    h.get.mockRejectedValue(new Error("boom"));
    open();
    expect((await screen.findAllByText("unavailable")).length).toBeGreaterThan(
      0,
    );
  });

  // A link names a capability by id; the list the page browsed may not hold it.
  describe("by id alone", () => {
    const openById = () =>
      render(
        <CapabilityDetailsPanel
          id={CAP.id}
          cap={null}
          usageEntry={undefined}
          onClose={vi.fn()}
          onRevoke={h.revoke}
          tenant="acme"
          onRevokeRun={h.revokeRun}
        />,
      );

    it("shows the capability its record holds", async () => {
      h.get.mockResolvedValue({ capability: CAP });
      openById();
      expect(screen.getByText("Reading the capability…")).toBeInTheDocument();
      expect(await screen.findByText(/agent:1/)).toBeInTheDocument();
    });

    it("says there is none when the record cannot be read", async () => {
      h.get.mockRejectedValue(new Error("not found"));
      openById();
      expect(
        await screen.findByText(/No capability cap-1 is on record/),
      ).toBeInTheDocument();
    });
  });

  // The page reads usage for the rows of its list alone, which by default
  // holds active capabilities: a revoked or expired one opened by its link
  // had none, and its usage read "loading…" for good.
  describe("usage the page did not read", () => {
    const openUnlisted = () =>
      render(
        <CapabilityDetailsPanel
          id={CAP.id}
          cap={CAP}
          usageEntry={undefined}
          onClose={vi.fn()}
          onRevoke={h.revoke}
          tenant="acme"
          onRevokeRun={h.revokeRun}
        />,
      );
    const value = (label: string) =>
      screen.getByText(label).nextElementSibling?.textContent;

    beforeEach(() => {
      h.get.mockResolvedValue({ capability: CAP });
    });

    it("is read by the panel", async () => {
      h.getUsage.mockResolvedValue({ requestCount: 3n, spent: undefined });
      openUnlisted();
      await waitFor(() => expect(value("Requests used")).toBe("3"));
      expect(h.getUsage).toHaveBeenCalledWith(
        { id: CAP.id },
        expect.anything(),
      );
    });

    it("says never used when the capability has no usage row", async () => {
      h.getUsage.mockRejectedValue(new ConnectError("no usage", Code.NotFound));
      openUnlisted();
      await waitFor(() => expect(value("Requests used")).toBe("never used"));
    });

    it("says unavailable when the read fails", async () => {
      h.getUsage.mockRejectedValue(new Error("boom"));
      openUnlisted();
      await waitFor(() => expect(value("Requests used")).toBe("unavailable"));
      expect(value("Spent")).toBe("unavailable");
    });

    it("is not read again when the page has it", async () => {
      open();
      await screen.findByText("Requests used");
      expect(h.getUsage).not.toHaveBeenCalled();
    });
  });

  it("stays closed with no id", () => {
    render(
      <CapabilityDetailsPanel
        id={null}
        cap={CAP}
        usageEntry="never"
        onClose={vi.fn()}
        onRevoke={h.revoke}
        tenant="acme"
        onRevokeRun={h.revokeRun}
      />,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(h.get).not.toHaveBeenCalled();
  });

  // Confirmed on the button that copied, not by a toast.
  it("says Copied on the identifier's copy button", async () => {
    const user = userEvent.setup();
    h.get.mockResolvedValue({ capability: CAP });
    open();
    await user.click(
      await screen.findByRole("button", { name: "Copy capability details id" }),
    );
    expect(
      await screen.findByRole("button", { name: /Copied/ }),
    ).toBeInTheDocument();
    expect(h.showNotification).not.toHaveBeenCalled();
  });

  // Revoking is a decision, confirmed by the page's own dialog.
  it("asks the page to revoke the capability shown", async () => {
    const user = userEvent.setup();
    h.revoke.mockReset();
    h.get.mockResolvedValue({ capability: CAP });
    open();
    await user.click(await screen.findByRole("button", { name: "Revoke" }));
    expect(h.revoke).toHaveBeenCalledWith(CAP);
  });

  // A capability exchanged under an OAuth grant links to the grant, where
  // revoking it revokes every capability of its session.
  describe("its OAuth grant", () => {
    const delegated = {
      ...CAP,
      session: "s-1",
      clientId: "agent-client",
      onBehalfOf: { kind: PrincipalKind.USER, tenantId: "t-1", subject: "u-1" },
    } as unknown as Capability;

    it("links the grant on its user's page", async () => {
      h.get.mockResolvedValue({ capability: delegated });
      open(delegated);
      const link = await screen.findByRole("link", { name: "s-1" });
      expect(link).toHaveAttribute("href", "/users/t-1/u-1?apps.id=s-1");
      expect(screen.getByText("agent-client")).toBeInTheDocument();
    });

    it("shows no grant for a capability issued outside one", async () => {
      h.get.mockResolvedValue({ capability: CAP });
      open();
      await screen.findByRole("group", { name: UNLOCKS });
      expect(screen.queryByText("OAuth grant")).not.toBeInTheDocument();
    });
  });

  // An agent run's capability offers its run: the run's capabilities listed,
  // and the whole run revoked, which the page confirms.
  it("offers an agent run's capabilities and its revocation", async () => {
    h.get.mockResolvedValue({});
    h.getUsage.mockResolvedValue({});
    h.listOperationGrants.mockResolvedValue({ grants: [] });
    const run = "0199-run";
    open({
      ...CAP,
      subject: { ...CAP.subject!, runId: run },
    } as unknown as Capability);
    const link = await screen.findByRole("link", {
      name: "Capabilities of this run",
    });
    expect(link.getAttribute("href")).toBe(
      `/tenants/acme/capabilities?run=${run}`,
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Revoke this run" }),
    );
    expect(h.revokeRun).toHaveBeenCalledWith(run);
  });

  it("offers no run for a capability that names none", async () => {
    h.get.mockResolvedValue({});
    h.getUsage.mockResolvedValue({});
    h.listOperationGrants.mockResolvedValue({ grants: [] });
    open();
    await screen.findByText("agent:1");
    expect(
      screen.queryByRole("button", { name: "Revoke this run" }),
    ).toBeNull();
  });
});
