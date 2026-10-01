import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// A failed Collection read left the picker holding only "New Collection" and
// the dropzone locked, which nudged the operator to create a Collection that
// may already exist.
const h = vi.hoisted(() => ({
  collections: [] as Array<Record<string, unknown>>,
  error: null as string | null,
  fetchAllCollections: vi.fn(() => Promise.resolve({ collections: [] })),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/hooks/useUpload", () => ({
  useUpload: () => ({ queue: [], uploadFile: vi.fn(), clearQueue: vi.fn() }),
}));
vi.mock("@/hooks/useCollections", () => ({
  useCollections: () => ({
    collections: h.collections,
    error: h.error,
    fetchAllCollections: h.fetchAllCollections,
  }),
}));

import UploadPage from "./page";

beforeEach(() => {
  h.error = null;
  h.fetchAllCollections.mockClear();
});

describe("UploadPage destination", () => {
  it("says the Collection list failed, and retries it", async () => {
    h.error = "unavailable: upstream";
    render(<UploadPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Collections could not be loaded/,
    );
    h.fetchAllCollections.mockClear();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(h.fetchAllCollections).toHaveBeenCalledOnce();
  });

  it("raises nothing when the list loaded", () => {
    render(<UploadPage />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
