import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({
  roles: ["platform.admin"] as string[],
  stats: null as null | { version: { version: string; commit: string } },
}));
// The new dashboard cards are tested on their own.
vi.mock("@/components/dashboard/FirstRunChecklist", () => ({
  FirstRunChecklist: () => null,
}));
vi.mock("@/components/dashboard/MyOperationsWidget", () => ({
  MyOperationsWidget: () => <div data-testid="my-operations" />,
}));
vi.mock("@/components/dashboard/QuotaAlertsWidget", () => ({
  QuotaAlertsWidget: () => <div data-testid="quota-alerts" />,
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { subject: "someone", roles: h.roles } }),
}));
vi.mock("@/context/StatsContext", () => ({
  useStats: () => ({ stats: h.stats }),
}));
vi.mock("@/lib/ui/build-info", () => ({
  uiBuildInfo: () => ({ version: "1.2.0", commit: "abc1234", buildTime: "" }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({ actions }: { actions?: React.ReactNode }) => (
    <div>{actions}</div>
  ),
}));
vi.mock("@/components/DashboardWidgets", () => ({
  DashboardWidgets: () => <div>admin widgets</div>,
}));
vi.mock("@/components/DispatcherStatsCard", () => ({
  DispatcherStatsCard: () => <div>dispatcher</div>,
}));

import DashboardPage from "./page";

describe("DashboardPage", () => {
  beforeEach(() => {
    h.roles = ["platform.admin"];
    h.stats = null;
  });

  it("shows the admin widgets to an admin-tier role", () => {
    render(<DashboardPage />);
    expect(screen.getByText("admin widgets")).toBeInTheDocument();
    expect(screen.getByText("dispatcher")).toBeInTheDocument();
    expect(screen.getByTestId("my-operations")).toBeInTheDocument();
    expect(screen.getByTestId("quota-alerts")).toBeInTheDocument();
    expect(screen.queryByText(/holds no admin role/)).toBeNull();
  });

  // Their every call would be refused; the dashboard says why instead of
  // rendering three failures and a token error.
  it("explains the missing role to a tenant.user instead", () => {
    h.roles = ["tenant.user"];
    render(<DashboardPage />);
    expect(screen.getByText(/holds no admin role/)).toBeInTheDocument();
    expect(screen.queryByText("admin widgets")).toBeNull();
    expect(screen.queryByText("dispatcher")).toBeNull();
    expect(screen.queryByTestId("quota-alerts")).toBeNull();
    expect(screen.queryByRole("link", { name: /upload/i })).toBeNull();
  });

  it("names the build once when the backend and UI match", () => {
    h.stats = { version: { version: "1.2.0", commit: "abc1234ffff" } };
    render(<DashboardPage />);
    expect(screen.getByText("build")).toBeInTheDocument();
    expect(screen.queryByText("backend")).toBeNull();
    expect(screen.getAllByText("1.2.0")).toHaveLength(1);
  });

  it("names both builds when they differ", () => {
    h.stats = { version: { version: "1.3.0", commit: "def5678" } };
    render(<DashboardPage />);
    expect(screen.getByText("backend")).toBeInTheDocument();
    expect(screen.getByText("ui")).toBeInTheDocument();
    expect(screen.getByText("1.3.0")).toBeInTheDocument();
    expect(screen.getByText("1.2.0")).toBeInTheDocument();
  });
});
