import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Characterization net for the cross-tenant collections page. Mock the three
// data hooks + scope + notifications and pin the shell, the on-mount fetch, and
// the provision dialog.
const h = vi.hoisted(() => ({
  fetchCollections: vi.fn(),
  createCollection: vi.fn(() => Promise.resolve()),
  deleteCollection: vi.fn(() => Promise.resolve()),
  fetchBuckets: vi.fn(),
  fetchBackends: vi.fn(),
  showNotification: vi.fn(),
}));

vi.mock("@/hooks/useCollections", () => ({
  useCollections: () => ({
    collections: [],
    nextPageToken: "",
    loading: false,
    fetchCollections: h.fetchCollections,
    createCollection: h.createCollection,
    deleteCollection: h.deleteCollection,
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

import CollectionsPage from "./page";

beforeEach(() => {
  h.fetchCollections.mockClear();
  h.createCollection.mockClear();
  h.deleteCollection.mockClear();
  h.showNotification.mockClear();
});

describe("CollectionsPage", () => {
  it("renders the header and a create action", () => {
    render(<CollectionsPage />);
    expect(
      screen.getByRole("heading", { name: "Collections" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New Collection/i }),
    ).toBeInTheDocument();
  });

  it("fetches collections on mount", () => {
    render(<CollectionsPage />);
    expect(h.fetchCollections).toHaveBeenCalled();
  });

  it("opens the provision dialog from the header action", async () => {
    render(<CollectionsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: /New Collection/i }),
    );
    expect(screen.getByText("Provision Collection")).toBeInTheDocument();
  });
});
