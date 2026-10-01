import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

// The picker's rows said "no backends configured", "no buckets visible" and
// showed only the signed-in tenant whether the list was empty, had failed to
// load, or was never asked for because the role has no admin plane.
const h = vi.hoisted(() => ({
  roles: ["platform.admin"] as string[],
  backends: {
    backends: [] as unknown[],
    error: null as string | null,
    fetchBackends: vi.fn(),
  },
  buckets: {
    buckets: [] as unknown[],
    loading: false,
    error: null as string | null,
    fetchBuckets: vi.fn(),
  },
  memberships: {
    memberships: [] as unknown[],
    loading: false,
    error: null as string | null,
    load: vi.fn(),
  },
}));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({
    tenantId: "01a0f4a9-7600-77a9-862e-11ea31d523ca",
    tenant: { displayName: "Platform" },
    backendId: null,
    bucketId: null,
    setBackend: vi.fn(),
    setBucket: vi.fn(),
    setScope: vi.fn(),
    isPickerOpen: true,
    setPickerOpen: vi.fn(),
  }),
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { roles: h.roles }, switchTenant: vi.fn() }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/hooks/useBackends", () => ({ useBackends: () => h.backends }));
vi.mock("@/hooks/useBuckets", () => ({ useBuckets: () => h.buckets }));
vi.mock("@/hooks/useMemberships", () => ({
  useMemberships: () => h.memberships,
}));

import { ScopePicker } from "./ScopePicker";

beforeEach(() => {
  h.roles = ["platform.admin"];
  h.backends.error = null;
  h.buckets.error = null;
  h.memberships.error = null;
});

// Each row's secondary text is part of its button's accessible name:
// "Switch backend — current: Any backend (<secondary>)".
const row = (label: string) =>
  screen.getByRole("button", { name: new RegExp(`^Switch ${label} `) });

describe("ScopePicker rows", () => {
  it("says a failed list could not be loaded, not that there is none", () => {
    h.backends.error = "unavailable";
    h.buckets.error = "unavailable";
    h.memberships.error = "memberships failed (503)";
    render(<ScopePicker />);
    expect(row("backend")).toHaveAccessibleName(/\(could not be loaded\)$/);
    expect(row("bucket")).toHaveAccessibleName(/\(could not be loaded\)$/);
    expect(row("tenant")).toHaveAccessibleName(
      /\(tenant list could not be loaded\)$/,
    );
  });

  it("says the lists are not the role's to see, rather than empty", () => {
    h.roles = [];
    render(<ScopePicker />);
    expect(row("backend")).toHaveAccessibleName(
      /\(not available to your role\)$/,
    );
    expect(row("bucket")).toHaveAccessibleName(
      /\(not available to your role\)$/,
    );
  });

  it("still says so when there really are none", () => {
    render(<ScopePicker />);
    expect(row("backend")).toHaveAccessibleName(/\(no backends configured\)$/);
    expect(row("bucket")).toHaveAccessibleName(/\(no buckets visible\)$/);
  });
});
