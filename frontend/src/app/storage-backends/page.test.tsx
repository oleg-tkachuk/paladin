import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Characterization net for the storage-backends page. useBackends auto-fetches
// on mount; mock it + notifications and pin the shell + create-dialog open.
const h = vi.hoisted(() => ({
  fetchBackends: vi.fn(),
  // Every filter the page asked the hook for, in order.
  filters: [] as string[],
  createBackend: vi.fn(() => Promise.resolve()),
  setBackendEnabled: vi.fn(() => Promise.resolve({})),
  setBackendReadOnly: vi.fn(() => Promise.resolve({})),
  setBackendMaintenance: vi.fn(() => Promise.resolve({})),
  showNotification: vi.fn(),
  backends: [] as unknown[],
  // The double used to omit `error` because the PAGE omitted it — a mock that
  // mirrors its caller instead of the hook it stands for cannot catch a field
  // the caller forgot, which is exactly what happened here.
  error: null as string | null,
}));

vi.mock("@/hooks/useBackends", () => ({
  useBackends: (options: { filter?: string } = {}) => {
    if (h.filters.at(-1) !== (options.filter ?? "")) {
      h.filters.push(options.filter ?? "");
    }
    return {
      backends: h.backends,
      loading: false,
      error: h.error,
      refetch: h.fetchBackends,
      createBackend: h.createBackend,
      setBackendEnabled: h.setBackendEnabled,
      setBackendReadOnly: h.setBackendReadOnly,
      setBackendMaintenance: h.setBackendMaintenance,
    };
  },
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
import {
  GUESSED_PROVIDER_HINT,
  GUESSED_PROVIDER_LABEL,
} from "@/lib/storageProvider";
import {
  FeatureSupport,
  StorageCompatibility,
  StorageFeature,
} from "@/gen/paladin/admin/v1/types_pb";

beforeEach(() => {
  h.fetchBackends.mockClear();
  h.createBackend.mockClear();
  h.setBackendEnabled.mockClear();
  h.setBackendReadOnly.mockClear();
  h.setBackendMaintenance.mockClear();
  h.showNotification.mockClear();
  h.backends = [];
  // A facet chosen in one test would otherwise open the next one narrowed.
  window.history.replaceState(null, "", "/storage-backends");
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
  features: [],
  compatibility: StorageCompatibility.UNVERIFIED,
  ...over,
});

// Every state change sits behind the row's menu.
const openRowMenu = (backendId: string) =>
  userEvent.click(
    screen.getByRole("button", { name: `Actions for ${backendId}` }),
  );

// One probed feature entry, as the server sends it.
const feature = (
  f: StorageFeature,
  support: FeatureSupport,
  required: boolean,
  enables: string,
) => ({
  feature: f,
  support,
  required,
  enables,
  message: "",
  checkedAt: { seconds: BigInt(1), nanos: 0 },
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

  it("drains an enabled backend from its row menu (001_initial_schema.sql)", async () => {
    h.backends = [makeBackend({ enabled: true, readOnly: false })];
    render(<StorageBackendsPage />);
    await openRowMenu("primary");
    await userEvent.click(screen.getByRole("menuitem", { name: "Drain" }));
    expect(h.setBackendReadOnly).toHaveBeenCalledWith("primary", true, "7");
  });

  it("leads a read-only backend's row with Draining and offers Undrain", async () => {
    h.backends = [makeBackend({ enabled: true, readOnly: true })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Draining")).toBeInTheDocument();
    expect(screen.queryByText("Enabled")).toBeNull();
    await openRowMenu("primary");
    expect(
      screen.getByRole("menuitem", { name: "Undrain" }),
    ).toBeInTheDocument();
  });

  it("shows a disabled backend as Disabled, with no drain item", async () => {
    h.backends = [makeBackend({ enabled: false, readOnly: true })];
    render(<StorageBackendsPage />);
    // Disabled outranks draining: a disabled backend refuses everything.
    expect(screen.getByText("Disabled")).toBeInTheDocument();
    expect(screen.queryByText("Draining")).toBeNull();
    await openRowMenu("primary");
    expect(screen.queryByRole("menuitem", { name: /drain/i })).toBeNull();
  });

  it("enables a disabled backend without asking", async () => {
    h.backends = [makeBackend({ enabled: false, resourceVersion: "4" })];
    render(<StorageBackendsPage />);
    await openRowMenu("primary");
    await userEvent.click(screen.getByRole("menuitem", { name: "Enable" }));
    expect(h.setBackendEnabled).toHaveBeenCalledWith("primary", true, "4");
  });

  it("disables a backend only once the operator confirms", async () => {
    h.backends = [makeBackend({ enabled: true, resourceVersion: "7" })];
    render(<StorageBackendsPage />);
    await openRowMenu("primary");
    await userEvent.click(screen.getByRole("menuitem", { name: /Disable/ }));

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Disable primary?");
    expect(h.setBackendEnabled).not.toHaveBeenCalled();

    await userEvent.click(
      within(dialog).getByRole("button", { name: "Disable" }),
    );
    await waitFor(() =>
      expect(h.setBackendEnabled).toHaveBeenCalledWith("primary", false, "7"),
    );
  });

  it("leaves the backend enabled when the disable is cancelled", async () => {
    h.backends = [makeBackend({ enabled: true })];
    render(<StorageBackendsPage />);
    await openRowMenu("primary");
    await userEvent.click(screen.getByRole("menuitem", { name: /Disable/ }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(h.setBackendEnabled).not.toHaveBeenCalled();
  });

  it("reports a refused state change as a toast", async () => {
    h.setBackendReadOnly.mockRejectedValueOnce(new Error("stale version"));
    h.backends = [makeBackend()];
    render(<StorageBackendsPage />);
    await openRowMenu("primary");
    await userEvent.click(screen.getByRole("menuitem", { name: "Drain" }));
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "error",
          title: "Could not change drain state",
        }),
      ),
    );
  });

  // A provider guessed from the endpoint was marked with a bare "?".
  it("says a provider was guessed from the endpoint, and not when it is set", () => {
    h.backends = [
      makeBackend({
        provider: "",
        endpoint: "http://seaweedfs-s3.storage:8333",
      }),
      makeBackend({
        backendId: "secondary",
        name: "storageBackends/secondary",
        provider: "garage",
      }),
    ];
    render(<StorageBackendsPage />);
    const guessed = screen.getByText(GUESSED_PROVIDER_LABEL);
    expect(guessed).toHaveAttribute("title", GUESSED_PROVIDER_HINT);
    expect(guessed.parentElement).toHaveTextContent(/^SeaweedFS \(guessed\)/);
    expect(screen.getAllByText(GUESSED_PROVIDER_LABEL)).toHaveLength(1);
    expect(screen.queryByText("?")).toBeNull();
  });

  it("shows the display name under the id, and marks an unnamed backend", () => {
    h.backends = [
      makeBackend({ backendId: "a", displayName: "Archive" }),
      makeBackend({ backendId: "b", displayName: "" }),
    ];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Archive")).toBeInTheDocument();
    expect(screen.getByText("unnamed")).toBeInTheDocument();
  });

  // Health (`001_initial_schema.sql`) — derived from the last TestBackend probe.
  it("shows Healthy when the last probe succeeded", () => {
    h.backends = [makeBackend({ healthStatus: "ok" })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Healthy")).toBeInTheDocument();
  });

  it("shows Probe failed (with the error) when the last probe failed", () => {
    h.backends = [
      makeBackend({
        healthStatus: "error",
        healthMessage: "connection refused",
      }),
    ];
    render(<StorageBackendsPage />);
    const health = screen.getByText("Probe failed");
    expect(health).toHaveAttribute("title", "connection refused");
  });

  it("shows Untested when the backend has never been probed", () => {
    h.backends = [makeBackend({ healthStatus: "unknown" })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Untested")).toBeInTheDocument();
  });

  // Compatibility (ADR-0026) — what the last feature probe found.
  it("marks a backend lacking a required feature incompatible, saying which", () => {
    h.backends = [
      makeBackend({
        compatibility: StorageCompatibility.INCOMPATIBLE,
        features: [
          feature(
            StorageFeature.CONDITIONAL_PUT,
            FeatureSupport.UNSUPPORTED,
            true,
            "refusing an upload that would replace an existing object",
          ),
        ],
      }),
    ];
    render(<StorageBackendsPage />);
    const badge = screen.getByText("Incompatible");
    expect(badge.getAttribute("title")).toContain(
      "Conditional PUT is unsupported",
    );
  });

  it("marks a compatible backend lacking an optional feature limited", () => {
    h.backends = [
      makeBackend({
        compatibility: StorageCompatibility.COMPATIBLE,
        features: [
          feature(
            StorageFeature.ANONYMOUS_READ_POLICY,
            FeatureSupport.UNSUPPORTED,
            false,
            "public collections",
          ),
        ],
      }),
    ];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Limited").getAttribute("title")).toContain(
      "public collections",
    );
  });

  // "Compatible" beside "S3-compatible" said one thing twice; the list
  // stays quiet when every feature is there.
  it("shows no features badge for a fully compatible backend", () => {
    h.backends = [
      makeBackend({ compatibility: StorageCompatibility.COMPATIBLE }),
    ];
    render(<StorageBackendsPage />);
    expect(screen.queryByText("Compatible")).toBeNull();
    expect(screen.getByText("primary")).toBeInTheDocument();
  });

  it("marks a backend never probed unverified", () => {
    h.backends = [makeBackend()];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Unverified")).toBeInTheDocument();
  });

  // Bulk actions — multi-select + client-side fan-out over the per-backend RPCs.
  it("bulk-disables the selection after one confirmation, each with its own resource_version", async () => {
    h.backends = [
      makeBackend({ backendId: "a", enabled: true, resourceVersion: "1" }),
      makeBackend({ backendId: "b", enabled: true, resourceVersion: "2" }),
    ];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByLabelText("Select a"));
    await userEvent.click(screen.getByLabelText("Select b"));
    await userEvent.click(screen.getByRole("button", { name: "Bulk disable" }));

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Disable 2 selected backends?");
    expect(h.setBackendEnabled).not.toHaveBeenCalled();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Disable" }),
    );

    await waitFor(() => expect(h.setBackendEnabled).toHaveBeenCalledTimes(2));
    expect(h.setBackendEnabled).toHaveBeenCalledWith("a", false, "1");
    expect(h.setBackendEnabled).toHaveBeenCalledWith("b", false, "2");
  });

  // The confirmation names the backends that stop serving, not only how many
  // were ticked: one already disabled is skipped, so it is not listed.
  it("lists the backends a bulk disable reaches, by id", async () => {
    h.backends = [
      makeBackend({ backendId: "a", enabled: true, resourceVersion: "1" }),
      makeBackend({ backendId: "b", enabled: false, resourceVersion: "2" }),
      makeBackend({ backendId: "c", enabled: true, resourceVersion: "3" }),
    ];
    render(<StorageBackendsPage />);
    for (const id of ["a", "b", "c"]) {
      await userEvent.click(screen.getByLabelText(`Select ${id}`));
    }
    await userEvent.click(screen.getByRole("button", { name: "Bulk disable" }));

    const dialog = screen.getByRole("alertdialog");
    const listed = within(dialog).getByRole("list", {
      name: "2 backends to disable",
    });
    expect(
      within(listed)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["a", "c"]);
    // Enabling undoes it, so nothing is typed.
    expect(within(dialog).queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("bulk-enables without asking", async () => {
    h.backends = [
      makeBackend({ backendId: "a", enabled: false, resourceVersion: "1" }),
    ];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByLabelText("Select a"));
    await userEvent.click(screen.getByRole("button", { name: "Bulk enable" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    await waitFor(() =>
      expect(h.setBackendEnabled).toHaveBeenCalledWith("a", true, "1"),
    );
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
    expect(h.showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Drain: 1 ok, 2 skipped" }),
    );
  });

  it("bulk action with no applicable backend is a no-op (no RPC)", async () => {
    // A single disabled backend: 'Bulk undrain' has nothing to change.
    h.backends = [makeBackend({ backendId: "a", enabled: false })];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByLabelText("Select a"));
    await userEvent.click(screen.getByRole("button", { name: "Bulk undrain" }));

    expect(h.setBackendReadOnly).not.toHaveBeenCalled();
    expect(h.showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Nothing to do" }),
    );
  });

  it("hides the bulk bar until something is selected", () => {
    h.backends = [makeBackend({ backendId: "a" })];
    render(<StorageBackendsPage />);
    expect(screen.queryByRole("button", { name: "Bulk disable" })).toBeNull();
  });

  // The bulk actions sit in the shared selection bar, which also drops the
  // selection.
  it("offers the bulk actions in the selection bar, and clears it", async () => {
    h.backends = [makeBackend({ backendId: "a" })];
    render(<StorageBackendsPage />);
    await userEvent.click(screen.getByRole("checkbox", { name: /select a$/i }));
    const bar = screen.getByRole("toolbar", { name: "Selection actions" });
    expect(bar).toHaveTextContent("1 selected");
    expect(
      within(bar).getByRole("button", { name: "Bulk disable" }),
    ).toBeInTheDocument();
    await userEvent.click(
      within(bar).getByRole("button", { name: "Clear selection" }),
    );
    expect(screen.queryByRole("toolbar")).not.toBeInTheDocument();
  });

  // Operator-set maintenance flag (`001_initial_schema.sql`) — advisory, orthogonal.
  it("flags a backend for maintenance from its row menu", async () => {
    h.backends = [makeBackend({ maintenance: false, resourceVersion: "7" })];
    render(<StorageBackendsPage />);
    await openRowMenu("primary");
    await userEvent.click(
      screen.getByRole("menuitem", { name: "Flag for maintenance" }),
    );
    expect(h.setBackendMaintenance).toHaveBeenCalledWith("primary", true, "7");
  });

  it("shows a Maintenance badge and a Clear item when flagged", async () => {
    h.backends = [makeBackend({ maintenance: true, resourceVersion: "7" })];
    render(<StorageBackendsPage />);
    expect(screen.getByText("Maintenance")).toBeInTheDocument();
    await openRowMenu("primary");
    await userEvent.click(
      screen.getByRole("menuitem", { name: "Clear maintenance" }),
    );
    expect(h.setBackendMaintenance).toHaveBeenCalledWith("primary", false, "7");
  });

  it("maintenance is settable on a DISABLED backend (advisory, orthogonal)", async () => {
    h.backends = [
      makeBackend({ backendId: "a", enabled: false, resourceVersion: "3" }),
    ];
    render(<StorageBackendsPage />);
    await openRowMenu("a");
    await userEvent.click(
      screen.getByRole("menuitem", { name: "Flag for maintenance" }),
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
    h.filters = [];

    typeInSearch("EU-North");
    act(() => {
      vi.advanceTimersByTime(400);
    });

    const sent = h.filters.at(-1);
    // ASCII-folded to match the server's search text, and one conjunct: a
    // disjunction pushes nothing into SQL, so matches past the first page
    // would come back reported as absent.
    expect(sent).toBe('search.contains("eu-north")');
    expect(sent).not.toContain("||");
  });

  it("debounces instead of listing once per keystroke", () => {
    render(<StorageBackendsPage />);
    h.filters = [];

    typeInSearch("m");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    typeInSearch("mi");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(h.filters.filter(Boolean)).toHaveLength(0);

    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(h.filters.filter(Boolean)).toHaveLength(1);
  });
});

// A long list is cut into pages; selecting all selects the page on screen.
describe("StorageBackendsPage pages", () => {
  it("shows fifty backends a page, and the next fifty on the next", async () => {
    h.backends = Array.from({ length: 55 }, (_, i) =>
      makeBackend({ backendId: `b-${String(i).padStart(2, "0")}` }),
    );
    render(<StorageBackendsPage />);
    expect(screen.getByText("b-49")).toBeInTheDocument();
    expect(screen.queryByText("b-50")).toBeNull();
    expect(screen.getByText("1–50 of 55")).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole("checkbox", { name: "Select all backends" }),
    );
    expect(screen.getByText(/50 selected/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect(screen.getByText("b-54")).toBeInTheDocument();
    expect(screen.queryByText("b-00")).toBeNull();
    expect(window.location.search).toContain("page=2");
  });
});

// The state and health facets narrow what the search loaded, each answer
// counted; several answers of one facet are any of them.
describe("StorageBackendsPage facets", () => {
  it("narrows to the chosen states without asking the server again", async () => {
    h.backends = [
      makeBackend({ backendId: "live" }),
      makeBackend({ backendId: "drained", readOnly: true }),
      makeBackend({ backendId: "off", enabled: false, maintenance: true }),
    ];
    render(<StorageBackendsPage />);
    h.filters = [];

    await userEvent.click(
      screen.getByRole("button", { name: "Filter by state" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: "Draining, 1 row" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: "Maintenance, 1 row" }),
    );
    await userEvent.keyboard("{Escape}");

    expect(screen.queryByText("live")).toBeNull();
    expect(screen.getByText("drained")).toBeInTheDocument();
    expect(screen.getByText("off")).toBeInTheDocument();
    expect(screen.getByText("2 of 3")).toBeInTheDocument();
    expect(window.location.search).toContain("state=draining%2Cmaintenance");
    expect(h.filters.filter(Boolean)).toEqual([]);
  });

  it("says nothing matches, rather than none registered, when a state is chosen", async () => {
    render(<StorageBackendsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by state" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Disabled/ }),
    );
    expect(
      await screen.findByText("No storage backends match your filters."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Register the first backend/ }),
    ).toBeNull();
  });

  it("shows every backend again from the empty state", async () => {
    h.backends = [makeBackend({ backendId: "live" })];
    window.history.replaceState(null, "", "/storage-backends?q=x&state=any");
    render(<StorageBackendsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: "Filter by state" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Disabled/ }),
    );
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByText("live")).toBeNull();

    await userEvent.click(
      screen.getByRole("button", { name: "Clear filters" }),
    );
    expect(screen.getByText("live")).toBeInTheDocument();
    expect(screen.getByRole("searchbox")).toHaveValue("");
  });
});

// A list that FAILED must not render as a list that is empty.
//
// /buckets already refuses to tell that lie — "No buckets yet" for a failed
// call invites the operator to create something that exists. This page took
// every field from useBackends except `error`, so a failed list said "No
// storage backends registered yet", and a second registration points a second
// row at the same store.
describe("StorageBackendsPage load failure", () => {
  afterEach(() => {
    h.error = null;
  });

  it("says the list is unknown, not empty", () => {
    h.error = "unavailable: connection refused";
    render(<StorageBackendsPage />);

    expect(
      screen.getByText(
        /could not be loaded — this list is unknown, not empty/i,
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("No storage backends yet."),
    ).not.toBeInTheDocument();
  });

  it("still says 'none yet' when the list really is empty", () => {
    // The other half: without this, hiding the empty state entirely would
    // pass the assertion above and break the page for a fresh install.
    render(<StorageBackendsPage />);
    expect(screen.getByText("No storage backends yet.")).toBeInTheDocument();
  });
});

describe("StorageBackendsPage sorting", () => {
  it("sorts the loaded backends by a column, then back", () => {
    h.backends = [
      makeBackend({ backendId: "bravo", region: "eu-west-1" }),
      makeBackend({ backendId: "alpha", region: "us-east-1", enabled: false }),
      makeBackend({ backendId: "charlie", region: "ap-south-1" }),
    ];
    render(<StorageBackendsPage />);
    const ids = () =>
      screen
        .getAllByRole("checkbox", { name: /^Select (?!all)/ })
        .map((c) => c.getAttribute("aria-label")?.replace("Select ", ""));
    fireEvent.click(screen.getByRole("button", { name: /^Region/ }));
    expect(ids()).toEqual(["charlie", "bravo", "alpha"]);
    fireEvent.click(screen.getByRole("button", { name: /^State/ }));
    // Disabled before Enabled, by the state's name.
    expect(ids()[0]).toBe("alpha");
  });
});
