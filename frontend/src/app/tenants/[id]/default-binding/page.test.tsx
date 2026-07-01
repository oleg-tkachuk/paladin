import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError } from "@connectrpc/connect";

// The Default Route page talks to tenantClient directly and reads bucket
// options from useBuckets. Mock both, plus tenant-context + Notification.
// Covers: NotFound → empty state, existing binding render, Set wiring, Clear
// wiring.
const h = vi.hoisted(() => ({
  get: vi.fn(),
  set: vi.fn(),
  clear: vi.fn(),
  fetchBuckets: vi.fn(),
  showNotification: vi.fn(),
  buckets: [] as Array<{ backendId: string; bucketName: string }>,
}));

vi.mock("@/lib/connect/client", () => ({
  tenantClient: {
    getTenantDefaultBinding: h.get,
    setTenantDefaultBinding: h.set,
    clearTenantDefaultBinding: h.clear,
  },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1", slug: "acme", displayName: "Acme" }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({ buckets: h.buckets, fetchBuckets: h.fetchBuckets }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import DefaultBindingPage from "./page";

const notFound = () => new ConnectError("no binding", Code.NotFound);

beforeEach(() => {
  h.get.mockReset();
  h.set.mockReset();
  h.clear.mockReset();
  h.fetchBuckets.mockReset();
  h.showNotification.mockReset();
  h.buckets = [
    { backendId: "primary", bucketName: "paladin-a" },
    { backendId: "primary", bucketName: "paladin-b" },
  ];
});

describe("DefaultBindingPage", () => {
  it("shows the empty state when no binding is set (NotFound)", async () => {
    h.get.mockRejectedValue(notFound());
    render(<DefaultBindingPage />);
    expect(
      await screen.findByText(/bare-name creation will be rejected/i),
    ).toBeInTheDocument();
    // Get is keyed by canonical tenant name, buckets scoped to the tenant.
    expect(h.get).toHaveBeenCalledWith(
      { name: "tenants/acme" },
      expect.objectContaining({ signal: expect.anything() }),
    );
    expect(h.fetchBuckets).toHaveBeenCalledWith(undefined, "", "", "t-1");
  });

  it("renders the current binding when one exists", async () => {
    h.get.mockResolvedValue({
      backendId: "primary",
      bucketName: "paladin-b",
      setBy: "admin@local",
    });
    render(<DefaultBindingPage />);
    expect(await screen.findByText("paladin-b")).toBeInTheDocument();
    expect(screen.getByText(/set by admin@local/i)).toBeInTheDocument();
    // Clear only appears when a binding exists.
    expect(screen.getByRole("button", { name: "Clear" })).toBeInTheDocument();
  });

  it("sets the binding from the selected bucket", async () => {
    // First load has no binding; the post-set refetch returns the new one.
    h.get.mockRejectedValueOnce(notFound()).mockResolvedValue({
      backendId: "primary",
      bucketName: "paladin-b",
      setBy: "admin@local",
    });
    h.set.mockResolvedValue({
      backendId: "primary",
      bucketName: "paladin-b",
      setBy: "admin@local",
    });
    render(<DefaultBindingPage />);
    await screen.findByText(/bare-name creation will be rejected/i);

    await userEvent.selectOptions(
      screen.getByLabelText("Default bucket"),
      "1", // index of paladin-b
    );
    await userEvent.click(screen.getByRole("button", { name: "Set" }));

    await waitFor(() =>
      expect(h.set).toHaveBeenCalledWith({
        name: "tenants/acme",
        backendId: "primary",
        bucketName: "paladin-b",
      }),
    );
    // The just-set binding is reflected in the UI.
    expect(await screen.findByText("paladin-b")).toBeInTheDocument();
  });

  it("clears the binding", async () => {
    // First load has a binding; the post-clear refetch reports NotFound.
    h.get
      .mockResolvedValueOnce({
        backendId: "primary",
        bucketName: "paladin-b",
        setBy: "admin@local",
      })
      .mockRejectedValue(notFound());
    h.clear.mockResolvedValue({});
    render(<DefaultBindingPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Clear" }));
    await waitFor(() =>
      expect(h.clear).toHaveBeenCalledWith({ name: "tenants/acme" }),
    );
    expect(
      await screen.findByText(/bare-name creation will be rejected/i),
    ).toBeInTheDocument();
  });
});
