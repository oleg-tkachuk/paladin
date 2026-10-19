import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({
  backends: {
    backends: [] as Array<Record<string, unknown>>,
    loading: false,
    error: null as string | null,
    fetchBackends: vi.fn(),
  },
}));
vi.mock("next/navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("next/navigation")>()),
  useParams: () => ({ backendId: "primary" }),
  usePathname: () => "/storage-backends/primary/features",
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/hooks/useBackends", () => ({ useBackends: () => h.backends }));

import BackendFeaturesPage from "./page";

beforeEach(() => {
  h.backends.backends = [];
  h.backends.error = null;
});

describe("BackendFeaturesPage", () => {
  it("lists the backend's probe on its own tab", () => {
    h.backends.backends = [
      {
        backendId: "primary",
        displayName: "Primary",
        kind: 2,
        compatibility: 2,
        features: [{ feature: 1, support: 1, required: true }],
      },
    ];
    render(<BackendFeaturesPage />);
    expect(screen.getByText("Required by Paladin")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "S3 features" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("says the backend list failed rather than that the backend is gone", () => {
    h.backends.error = "boom";
    render(<BackendFeaturesPage />);
    expect(screen.queryByText("Backend not found.")).not.toBeInTheDocument();
    expect(screen.getByText(/boom/)).toBeInTheDocument();
  });

  it("says a backend that is not there is not found", () => {
    render(<BackendFeaturesPage />);
    expect(screen.getByText("Backend not found.")).toBeInTheDocument();
  });
});
