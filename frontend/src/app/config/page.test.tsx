import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

// The snapshot read a failed tenant or backend list as "0" and "no tenants
// yet" / "none registered" — a claim about the platform it could not make.
const h = vi.hoisted(() => ({
  tenants: {
    tenants: [] as unknown[],
    loading: false,
    error: null as string | null,
    fetchTenants: vi.fn(),
  },
  backends: {
    backends: [] as unknown[],
    loading: false,
    error: null as string | null,
    fetchBackends: vi.fn(),
  },
}));

vi.mock("@/hooks/useTenants", () => ({ useTenants: () => h.tenants }));
vi.mock("@/hooks/useBackends", () => ({ useBackends: () => h.backends }));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    user: {
      roles: ["platform.admin"],
      tenantId: "01a0f4a9-7600-77a9-862e-11ea31d523ca",
      subject: "admin",
    },
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/hooks/useConfig", () => ({
  useConfig: () => ({ config: "", path: "", loading: false, error: null }),
}));

import ConfigPage from "./page";

beforeEach(() => {
  h.tenants.error = null;
  h.backends.error = null;
});

describe("ConfigPage snapshot", () => {
  it("shows a failed count as unknown, not as zero", () => {
    h.tenants.error = "unavailable: upstream";
    h.backends.error = "unavailable: upstream";
    render(<ConfigPage />);
    expect(screen.queryByText("no tenants yet")).not.toBeInTheDocument();
    expect(screen.queryByText("none registered")).not.toBeInTheDocument();
    expect(screen.getAllByText("could not be loaded")).toHaveLength(2);
  });

  it("still says so when there really are none", () => {
    render(<ConfigPage />);
    expect(screen.getByText("no tenants yet")).toBeInTheDocument();
    expect(screen.getByText("none registered")).toBeInTheDocument();
  });
});
