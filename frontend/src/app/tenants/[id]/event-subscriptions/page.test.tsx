import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Protective net for decomposing the event-subscriptions page (helpers/form
// extraction, then the editor dialog). The page drives eventSubscriptionClient
// directly, so mock that + the tenant context, notifications, and the live CEL
// hook (so it never hits the network and never blocks submit). Covers the
// behaviors the extraction must preserve: header, editor open/validate/save,
// row edit-prefill, delete, and the optimistic disable toggle.
const h = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  del: vi.fn(),
  test: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({
  eventSubscriptionClient: {
    listSubscriptions: h.list,
    createSubscription: h.create,
    updateSubscription: h.update,
    deleteSubscription: h.del,
    testSubscription: h.test,
  },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1", displayName: "Acme" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
// Keep the CEL filter out of the way — never validating/invalid, so it never
// gates the Create/Save button during these tests.
vi.mock("@/hooks/useCELValidation", () => ({
  useCELValidation: () => ({ status: "valid" }),
}));
vi.mock("@/components/ui/CELIndicator", () => ({
  CELIndicator: () => null,
}));

import EventsPage from "./page";

const makeSub = (name: string, url: string) => ({
  $typeName: "paladin.admin.v1.EventSubscription",
  name,
  tenantId: "t-1",
  filter: "",
  disabled: false,
  resourceVersion: "v1",
  sink: {
    $typeName: "paladin.admin.v1.EventSink",
    target: {
      case: "http" as const,
      value: {
        $typeName: "paladin.admin.v1.HttpSink",
        url,
        signingSecretRef: "",
        maxAttempts: 5,
        format: "",
      },
    },
  },
});

const newSubButtons = () =>
  screen.getAllByRole("button", { name: /New subscription/i });

beforeEach(() => {
  h.list.mockResolvedValue({ subscriptions: [] });
  h.create.mockReset();
  h.update.mockReset();
  h.del.mockReset();
  h.test.mockReset();
  h.showNotification.mockReset();
});

describe("EventsPage", () => {
  it("renders the header and a create action", async () => {
    render(<EventsPage />);
    expect(
      screen.getByRole("heading", { name: "Event subscriptions" }),
    ).toBeInTheDocument();
    expect(newSubButtons().length).toBeGreaterThan(0);
  });

  it("opens the editor from the header action", async () => {
    render(<EventsPage />);
    await userEvent.click(newSubButtons()[0]);
    expect(
      screen.getByRole("heading", { name: "New event subscription" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^URL/)).toBeInTheDocument();
  });

  it("does not create when the URL is invalid", async () => {
    render(<EventsPage />);
    await userEvent.click(newSubButtons()[0]);
    await userEvent.click(
      screen.getByRole("button", { name: "Create subscription" }),
    );
    expect(h.create).not.toHaveBeenCalled();
    expect(screen.getByText(/valid http\(s\):\/\/ URL/i)).toBeInTheDocument();
  });

  it("creates a subscription when the form is valid", async () => {
    h.create.mockResolvedValue(makeSub("sub-new", "https://example.com/hook"));
    render(<EventsPage />);
    await userEvent.click(newSubButtons()[0]);
    await userEvent.type(
      screen.getByLabelText(/^URL/),
      "https://example.com/hook",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Create subscription" }),
    );
    await waitFor(() =>
      expect(h.create).toHaveBeenCalledWith(
        expect.objectContaining({ parent: "tenants/t-1" }),
      ),
    );
  });

  // It used to close into a toast; the editor now keeps the form and says why.
  it("keeps a failed create in the editor, with the reason", async () => {
    h.create.mockRejectedValue(new Error("boom"));
    render(<EventsPage />);
    await userEvent.click(newSubButtons()[0]);
    await userEvent.type(
      screen.getByLabelText(/^URL/),
      "https://example.com/hook",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Create subscription" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
    expect(screen.getByLabelText(/^URL/)).toHaveValue(
      "https://example.com/hook",
    );
  });

  it("opens the editor prefilled from the row menu", async () => {
    h.list.mockResolvedValue({
      subscriptions: [makeSub("sub-1", "https://hook.example.com/x")],
    });
    render(<EventsPage />);
    await userEvent.click(
      await screen.findByText("Actions for subscription sub-1"),
    );
    await userEvent.click(screen.getByText("Edit"));
    expect(
      screen.getByRole("heading", { name: "Edit subscription" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^URL/)).toHaveValue(
      "https://hook.example.com/x",
    );
  });

  it("deletes a subscription from the row menu", async () => {
    h.list.mockResolvedValue({
      subscriptions: [makeSub("sub-1", "https://hook.example.com/x")],
    });
    h.del.mockResolvedValue({});
    render(<EventsPage />);
    await userEvent.click(
      await screen.findByText("Actions for subscription sub-1"),
    );
    await userEvent.click(screen.getByText("Delete"));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(h.del).toHaveBeenCalledWith(
        expect.objectContaining({ name: "sub-1" }),
      ),
    );
  });

  it("toggles disabled from the row menu", async () => {
    h.list.mockResolvedValue({
      subscriptions: [makeSub("sub-1", "https://hook.example.com/x")],
    });
    h.update.mockResolvedValue({
      ...makeSub("sub-1", "https://hook.example.com/x"),
      disabled: true,
    });
    render(<EventsPage />);
    await userEvent.click(
      await screen.findByText("Actions for subscription sub-1"),
    );
    await userEvent.click(screen.getByText("Disable"));
    await waitFor(() =>
      expect(h.update).toHaveBeenCalledWith(
        expect.objectContaining({ name: "sub-1" }),
      ),
    );
  });
});
