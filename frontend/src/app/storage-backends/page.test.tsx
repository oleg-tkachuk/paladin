import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Characterization net for the storage-backends page. useBackends auto-fetches
// on mount; mock it + notifications and pin the shell + create-dialog open.
const h = vi.hoisted(() => ({
  fetchBackends: vi.fn(),
  createBackend: vi.fn(() => Promise.resolve()),
  setBackendEnabled: vi.fn(() => Promise.resolve({})),
  setBackendReadOnly: vi.fn(() => Promise.resolve({})),
  setBackendMaintenance: vi.fn(() => Promise.resolve({})),
  showNotification: vi.fn(),
  backends: [] as unknown[],
}));

vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({
    backends: h.backends,
    loading: false,
    fetchBackends: h.fetchBackends,
    createBackend: h.createBackend,
    setBackendEnabled: h.setBackendEnabled,
    setBackendReadOnly: h.setBackendReadOnly,
    setBackendMaintenance: h.setBackendMaintenance,
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
  h.setBackendEnabled.mockClear();
  h.setBackendReadOnly.mockClear();
  h.setBackendMaintenance.mockClear();
  h.showNotification.mockClear();
  h.backends = [];
});

const makeBackend = (over: Record<string, unknown> = {}) => ({
  backendId: "primary",
  displayName: "Primary",
  kind: 2,
  endpoint: "https://s3.local",
  region: "us-east-1",
  enabled: true,
  readOnly: false,
  resourceVersion: "7",
  name: "storageBackends/primary",
  healthStatus: "unknown",
  healthMessage: "",
  maintenance: false,
  ...over,
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

  it("drains an enabled backend via setBackendReadOnly (migration 047)", async () => {
    h.backends = [makeBackend({ enabled: true, readOnly: false })];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByRole("button", { name: "Drain" }));
    expect(h.setBackendReadOnly).toHaveBeenCalledWith("primary", true, "7");
  });

  it("shows a Draining badge + Undrain for a read-only backend", () => {
    h.backends = [makeBackend({ enabled: true, readOnly: true })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Draining")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Undrain" })).toBeInTheDocument();
  });

  it("hides the drain control for a disabled backend", () => {
    h.backends = [makeBackend({ enabled: false, readOnly: false })];
    render(<StorageBackendsPage />);
    expect(screen.queryByRole("button", { name: "Drain" })).toBeNull();
  });

  // Health badges (migration 048) — derived from the last TestBackend probe.
  it("shows a Healthy badge when the last probe succeeded", () => {
    h.backends = [makeBackend({ healthStatus: "ok" })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Healthy")).toBeInTheDocument();
  });

  it("shows a Probe-failed badge (with the error) when the last probe failed", () => {
    h.backends = [
      makeBackend({
        healthStatus: "error",
        healthMessage: "connection refused",
      }),
    ];
    render(<StorageBackendsPage />);
    const badge = screen.getByText("Probe failed");
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveAttribute("title", "connection refused");
  });

  it("shows an Untested badge when the backend has never been probed", () => {
    h.backends = [makeBackend({ healthStatus: "unknown" })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Untested")).toBeInTheDocument();
  });

  // Bulk actions — multi-select + client-side fan-out over the per-backend RPCs.
  it("bulk-disables the selected backends, each with its own resource_version", async () => {
    h.backends = [
      makeBackend({ backendId: "a", enabled: true, resourceVersion: "1" }),
      makeBackend({ backendId: "b", enabled: true, resourceVersion: "2" }),
    ];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByLabelText("Select a"));
    await userEvent.click(screen.getByLabelText("Select b"));
    await userEvent.click(screen.getByRole("button", { name: "Bulk disable" }));

    await waitFor(() => expect(h.setBackendEnabled).toHaveBeenCalledTimes(2));
    expect(h.setBackendEnabled).toHaveBeenCalledWith("a", false, "1");
    expect(h.setBackendEnabled).toHaveBeenCalledWith("b", false, "2");
  });

  it("select-all then bulk-drain fans out to enabled backends only (skips no-ops)", async () => {
    h.backends = [
      makeBackend({
        backendId: "a",
        enabled: true,
        readOnly: false,
        resourceVersion: "1",
      }),
      // already draining → drain is a no-op, skipped
      makeBackend({
        backendId: "b",
        enabled: true,
        readOnly: true,
        resourceVersion: "2",
      }),
      // disabled → drain does not apply, skipped
      makeBackend({
        backendId: "c",
        enabled: false,
        readOnly: false,
        resourceVersion: "3",
      }),
    ];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByLabelText("Select all backends"));
    await userEvent.click(screen.getByRole("button", { name: "Bulk drain" }));

    await waitFor(() => expect(h.setBackendReadOnly).toHaveBeenCalledTimes(1));
    expect(h.setBackendReadOnly).toHaveBeenCalledWith("a", true, "1");
  });

  it("bulk action with no applicable backend is a no-op (no RPC)", async () => {
    // A single disabled backend: 'Bulk disable' has nothing to change.
    h.backends = [makeBackend({ backendId: "a", enabled: false })];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByLabelText("Select a"));
    await userEvent.click(screen.getByRole("button", { name: "Bulk disable" }));

    expect(h.setBackendEnabled).not.toHaveBeenCalled();
    expect(h.showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Nothing to do" }),
    );
  });

  it("hides the bulk bar until something is selected", () => {
    h.backends = [makeBackend({ backendId: "a" })];
    render(<StorageBackendsPage />);
    expect(screen.queryByRole("button", { name: "Bulk disable" })).toBeNull();
  });

  // Operator-set maintenance flag (migration 049) — advisory, orthogonal.
  it("flags a backend for maintenance via setBackendMaintenance", async () => {
    h.backends = [makeBackend({ maintenance: false, resourceVersion: "7" })];
    render(<StorageBackendsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: "Flag primary for maintenance" }),
    );
    expect(h.setBackendMaintenance).toHaveBeenCalledWith("primary", true, "7");
  });

  it("shows a Maintenance badge and a Clear control when flagged", () => {
    h.backends = [makeBackend({ maintenance: true })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Maintenance")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Clear maintenance on primary" }),
    ).toBeInTheDocument();
  });

  it("maintenance is settable on a DISABLED backend (advisory, orthogonal)", async () => {
    // Unlike drain, the maintenance control is not gated on enabled.
    h.backends = [
      makeBackend({ backendId: "a", enabled: false, resourceVersion: "3" }),
    ];
    render(<StorageBackendsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: "Flag a for maintenance" }),
    );
    expect(h.setBackendMaintenance).toHaveBeenCalledWith("a", true, "3");
  });

  it("bulk-flags maintenance across the selection, skipping already-flagged", async () => {
    h.backends = [
      makeBackend({ backendId: "a", maintenance: false, resourceVersion: "1" }),
      makeBackend({ backendId: "b", maintenance: true, resourceVersion: "2" }),
    ];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByLabelText("Select all backends"));
    await userEvent.click(
      screen.getByRole("button", { name: "Bulk maintenance" }),
    );
    await waitFor(() =>
      expect(h.setBackendMaintenance).toHaveBeenCalledTimes(1),
    );
    expect(h.setBackendMaintenance).toHaveBeenCalledWith("a", true, "1");
  });
});

// The backend search moved to the API. It used to fetch every backend and
// narrow the array in the browser, matching four fields — id, display name,
// region, endpoint — so the derived `search` field joins the same four. A
// version covering only id and display name would have shipped as a search
// that quietly stopped finding backends by region.
describe("StorageBackendsPage search", () => {
  const typeInSearch = (value: string) =>
    act(() => {
      fireEvent.change(screen.getByPlaceholderText(/search/i), {
        target: { value },
      });
    });

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("sends the query to the API as one CEL conjunct", () => {
    render(<StorageBackendsPage />);
    h.fetchBackends.mockClear();

    typeInSearch("EU-North");
    act(() => {
      vi.advanceTimersByTime(400);
    });

    const sent = h.fetchBackends.mock.calls.at(-1)?.[0];
    // ASCII-folded to match the server's search text, and one conjunct: a
    // disjunction pushes nothing into SQL, so matches past the first page
    // would come back reported as absent.
    expect(sent).toBe('search.contains("eu-north")');
    expect(sent).not.toContain("||");
  });

  it("debounces instead of listing once per keystroke", () => {
    render(<StorageBackendsPage />);
    h.fetchBackends.mockClear();

    typeInSearch("m");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    typeInSearch("mi");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(h.fetchBackends.mock.calls.filter((c) => c[0])).toHaveLength(0);

    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(h.fetchBackends.mock.calls.filter((c) => c[0])).toHaveLength(1);
  });
});
