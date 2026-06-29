import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Characterization net for the api-tokens page (legacy user-scoped PATs, an
// iam.ApiKey distinct from the admin-plane M2M tokens). Mock the apiKey client
// + scope + notifications and pin the shell, the on-mount list, and the create
// dialog.
const h = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  revoke: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  apiKeyClient: {
    listApiKeys: h.list,
    createApiKey: h.create,
    revokeApiKey: h.revoke,
  },
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: "t-1" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({
    title,
    actions,
  }: {
    title: string;
    actions?: React.ReactNode;
  }) => (
    <div>
      <h1>{title}</h1>
      {actions}
    </div>
  ),
}));

import ApiTokensPage from "./page";

beforeEach(() => {
  h.list.mockResolvedValue({ apiKeys: [] });
  h.create.mockReset();
  h.revoke.mockReset();
  h.showNotification.mockReset();
});

describe("ApiTokensPage", () => {
  it("renders the header and a create action", () => {
    render(<ApiTokensPage />);
    expect(
      screen.getByRole("heading", { name: "API Tokens" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New token/i }),
    ).toBeInTheDocument();
  });

  it("lists API tokens on mount", async () => {
    render(<ApiTokensPage />);
    await waitFor(() => expect(h.list).toHaveBeenCalled());
  });

  it("opens the create dialog from the header action", async () => {
    render(<ApiTokensPage />);
    await userEvent.click(screen.getByRole("button", { name: /New token/i }));
    expect(screen.getByText("New API token")).toBeInTheDocument();
  });
});
