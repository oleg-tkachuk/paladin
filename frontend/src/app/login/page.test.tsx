import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Page-test pattern: mock next/navigation + the page's context hook(s) via
// vi.hoisted (the factories run before imports). Spies + mutable state live
// on `h` so each test can drive router/auth without a real provider tree.
const h = vi.hoisted(() => ({
  replace: vi.fn(),
  login: vi.fn(),
  state: {
    search: new URLSearchParams(""),
    status: "unauthenticated" as string,
    error: null as string | null,
  },
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: h.replace }),
  useSearchParams: () => h.state.search,
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    status: h.state.status,
    error: h.state.error,
    login: h.login,
  }),
}));

import LoginPage from "./page";

beforeEach(() => {
  h.replace.mockClear();
  h.login.mockReset();
  h.state.search = new URLSearchParams("");
  h.state.status = "unauthenticated";
  h.state.error = null;
});

describe("LoginPage", () => {
  it("renders the sign-in form", () => {
    render(<LoginPage />);
    expect(
      screen.getByRole("heading", { name: "Paladin" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Email or username")).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
  });

  it("submits the typed credentials and redirects to ?next on success", async () => {
    h.state.search = new URLSearchParams("next=/tenants");
    h.login.mockResolvedValue(undefined);
    render(<LoginPage />);
    await userEvent.type(screen.getByLabelText("Email or username"), "admin");
    await userEvent.type(screen.getByLabelText("Password"), "pw123");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(h.login).toHaveBeenCalledWith("admin", "pw123");
    expect(h.replace).toHaveBeenCalledWith("/tenants");
  });

  it("surfaces the login error and does not redirect", async () => {
    h.login.mockRejectedValue(new Error("invalid credentials"));
    render(<LoginPage />);
    await userEvent.type(screen.getByLabelText("Email or username"), "admin");
    await userEvent.type(screen.getByLabelText("Password"), "bad");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("invalid credentials")).toBeInTheDocument();
    expect(h.replace).not.toHaveBeenCalled();
  });

  it("bounces an already-authenticated visitor out of /login", () => {
    h.state.status = "authenticated";
    h.state.search = new URLSearchParams("next=/buckets");
    render(<LoginPage />);
    expect(h.replace).toHaveBeenCalledWith("/buckets");
  });
});
