import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Characterization net for the storage-backends page. useBackends auto-fetches
// on mount; mock it + notifications and pin the shell + create-dialog open.
const h = vi.hoisted(() => ({
  fetchBackends: vi.fn(),
  createBackend: vi.fn(() => Promise.resolve()),
  showNotification: vi.fn(),
}));

vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({
    backends: [],
    loading: false,
    fetchBackends: h.fetchBackends,
    createBackend: h.createBackend,
  }),
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

import StorageBackendsPage from "./page";

beforeEach(() => {
  h.fetchBackends.mockClear();
  h.createBackend.mockClear();
  h.showNotification.mockClear();
});

describe("StorageBackendsPage", () => {
  it("renders the header and a create action", () => {
    render(<StorageBackendsPage />);
    expect(
      screen.getByRole("heading", { name: "Storage Backends" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New backend/i }),
    ).toBeInTheDocument();
  });

  it("opens the register-backend dialog from the header action", async () => {
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New backend/i }));
    expect(screen.getByText("Register storage backend")).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText("aws-eu, r2-global, minio-dev"),
    ).toBeInTheDocument();
  });
});
