import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Protective net for decomposing the m2m-tokens page (consts, then the create
// + revoke dialogs). The page talks to apiTokenClient directly, so mock that +
// the tenant context + notifications. Covers the behaviors the extraction must
// preserve: header, create-dialog open, the empty-name guard, the create
// wiring, the one-shot token reveal, and revoke from a row.
const h = vi.hoisted(() => ({
  list: vi.fn(),
  getUsage: vi.fn(),
  create: vi.fn(),
  revoke: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  apiTokenClient: {
    list: h.list,
    getUsage: h.getUsage,
    create: h.create,
    revoke: h.revoke,
  },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1", displayName: "Acme" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import M2MTokensPage from "./page";

const NAME_PLACEHOLDER = /ci-uploader/i;
const newTokenBtn = () => screen.getByRole("button", { name: "New token" });

// `name` is the RESOURCE name now — tenants/{t}/apiTokens/{id} — and the
// operator's label is display_name. Revoke is addressed by the former, which
// is the whole reason the conversion happened: a bare id carries no tenant, so
// it cannot be scoped past row-level security.
const makeToken = (id: string, displayName: string) => ({
  id,
  name: `tenants/t-1/apiTokens/${id}`,
  prefix: "abcd",
  displayName,
  scopes: [],
  audience: ["data"],
  rateLimitRpm: 0,
  revokedAt: undefined,
  expiresAt: undefined,
  lastUsedAt: undefined,
});

beforeEach(() => {
  h.list.mockResolvedValue({ apiTokens: [] });
  h.getUsage.mockResolvedValue({
    limitRpm: 0,
    weightedCount: 0,
    currentBucketCount: 0n,
    windowResetsAt: undefined,
  });
  h.create.mockReset();
  h.revoke.mockReset();
  h.showNotification.mockReset();
});

describe("M2MTokensPage", () => {
  it("renders the header and a create action", () => {
    render(<M2MTokensPage />);
    expect(
      screen.getByRole("heading", { name: "M2M Tokens" }),
    ).toBeInTheDocument();
    expect(newTokenBtn()).toBeInTheDocument();
  });

  it("opens the create dialog from the header action", async () => {
    render(<M2MTokensPage />);
    await userEvent.click(newTokenBtn());
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByPlaceholderText(NAME_PLACEHOLDER)).toBeInTheDocument();
  });

  it("does not call create when the name is empty", async () => {
    render(<M2MTokensPage />);
    await userEvent.click(newTokenBtn());
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    expect(h.create).not.toHaveBeenCalled();
  });

  it("calls apiTokenClient.create when a name is provided", async () => {
    // Reject so the reveal panel render is skipped; we only assert the submit
    // wires through to the client.
    h.create.mockRejectedValue(new Error("boom"));
    render(<M2MTokensPage />);
    await userEvent.click(newTokenBtn());
    await userEvent.type(
      screen.getByPlaceholderText(NAME_PLACEHOLDER),
      "ci-uploader",
    );
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() =>
      expect(h.create).toHaveBeenCalledWith(
        expect.objectContaining({
          parent: "tenants/t-1",
          displayName: "ci-uploader",
        }),
      ),
    );
  });

  it("reveals the token once on a successful create", async () => {
    h.create.mockResolvedValue({
      token: "paladin_pat_secret123",
      apiToken: { ...makeToken("tok-new", "ci-uploader"), prefix: "abcd" },
    });
    render(<M2MTokensPage />);
    await userEvent.click(newTokenBtn());
    await userEvent.type(
      screen.getByPlaceholderText(NAME_PLACEHOLDER),
      "ci-uploader",
    );
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);

    expect(
      await screen.findByDisplayValue("paladin_pat_secret123"),
    ).toBeInTheDocument();
    expect(h.showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ type: "success", title: "Token created" }),
    );
  });

  it("revokes a token from the row action", async () => {
    h.list.mockResolvedValue({
      apiTokens: [makeToken("tok-1", "ci-uploader")],
    });
    h.revoke.mockResolvedValue({});
    render(<M2MTokensPage />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Revoke ci-uploader/i }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith(
        expect.objectContaining({ name: "tenants/t-1/apiTokens/tok-1" }),
      ),
    );
  });
});

// A failed list fell through to "No M2M tokens yet. Create the first one";
// the toast that said otherwise was gone in seconds.
describe("M2MTokensPage failed list", () => {
  it("says the list could not be loaded, not that there are none", async () => {
    h.list.mockRejectedValue(new Error("unavailable"));
    render(<M2MTokensPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /M2M tokens could not be loaded/,
    );
    expect(screen.queryByText(/No M2M tokens yet/)).toBeNull();
  });
});
