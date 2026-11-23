import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AuthStatus } from "@/context/AuthContext";
import { SESSION_UNAVAILABLE_MESSAGE } from "@/lib/session/check";

const auth = vi.hoisted(() => ({ status: "loading" as AuthStatus }));
const replace = vi.hoisted(() => vi.fn());

vi.mock("@/context/AuthContext", () => ({ useAuth: () => auth }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace }),
  usePathname: () => "/buckets",
}));

import { AuthGate } from "./AuthGate";

const PAGE = "the page";

const renderGate = () =>
  render(
    <AuthGate publicRoutes={new Set(["/login"])}>
      <p>{PAGE}</p>
    </AuthGate>,
  );

describe("AuthGate", () => {
  beforeEach(() => {
    replace.mockReset();
  });

  it("renders the page for a signed-in user", () => {
    auth.status = "authenticated";
    renderGate();
    expect(screen.getByText(PAGE)).toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
  });

  it("sends a signed-out user to /login", () => {
    auth.status = "unauthenticated";
    renderGate();
    expect(screen.queryByText(PAGE)).not.toBeInTheDocument();
    expect(replace).toHaveBeenCalledWith(expect.stringMatching(/^\/login/));
  });

  // The role could not check the session: the user is not signed out, and
  // there is no user to show the page to yet.
  it("says sign-in is unavailable, without the page or a redirect", () => {
    auth.status = "unavailable";
    renderGate();
    expect(screen.getByRole("status")).toHaveTextContent(
      SESSION_UNAVAILABLE_MESSAGE,
    );
    expect(screen.queryByText(PAGE)).not.toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
  });
});
