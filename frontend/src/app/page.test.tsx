import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ roles: ["platform.admin"] as string[] }));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { subject: "someone", roles: h.roles } }),
}));
vi.mock("@/context/StatsContext", () => ({
  useStats: () => ({ stats: null }),
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
  });

  it("shows the admin widgets to an admin-tier role", () => {
    render(<DashboardPage />);
    expect(screen.getByText("admin widgets")).toBeInTheDocument();
    expect(screen.getByText("dispatcher")).toBeInTheDocument();
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
    expect(screen.queryByRole("link", { name: /upload/i })).toBeNull();
  });
});
