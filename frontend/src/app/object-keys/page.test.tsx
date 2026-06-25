import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Characterization net for the cross-tenant object-keys page. Mock the three
// data hooks + scope + notifications and pin the shell, the on-mount fetch, and
// the provision dialog.
const h = vi.hoisted(() => ({
  fetchObjectKeys: vi.fn(),
  createObjectKey: vi.fn(() => Promise.resolve()),
  deleteObjectKey: vi.fn(() => Promise.resolve()),
  fetchBuckets: vi.fn(),
  fetchBackends: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/hooks/useObjectKeys", () => ({
  useObjectKeys: () => ({
    objectKeys: [],
    nextPageToken: "",
    loading: false,
    fetchObjectKeys: h.fetchObjectKeys,
    createObjectKey: h.createObjectKey,
    deleteObjectKey: h.deleteObjectKey,
  }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: [],
    loading: false,
    fetchBuckets: h.fetchBuckets,
  }),
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({ backends: [], fetchBackends: h.fetchBackends }),
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: "t-1", tenant: { displayName: "Acme" } }),
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

import ObjectKeysPage from "./page";

beforeEach(() => {
  h.fetchObjectKeys.mockClear();
  h.createObjectKey.mockClear();
  h.deleteObjectKey.mockClear();
  h.showNotification.mockClear();
});

describe("ObjectKeysPage", () => {
  it("renders the header and a create action", () => {
    render(<ObjectKeysPage />);
    expect(
      screen.getByRole("heading", { name: "Object Keys" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New ObjectKey/i }),
    ).toBeInTheDocument();
  });

  it("fetches object keys on mount", () => {
    render(<ObjectKeysPage />);
    expect(h.fetchObjectKeys).toHaveBeenCalled();
  });

  it("opens the provision dialog from the header action", async () => {
    render(<ObjectKeysPage />);
    await userEvent.click(
      screen.getByRole("button", { name: /New ObjectKey/i }),
    );
    expect(screen.getByText("Provision ObjectKey")).toBeInTheDocument();
  });
});
