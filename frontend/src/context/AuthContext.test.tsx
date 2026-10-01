import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AuthProvider, useAuth } from "./AuthContext";

const USER = { subject: "admin", tenantId: "t-1", roles: ["platform.admin"] };

function Status() {
  const { status, login } = useAuth();
  return (
    <>
      <p>status: {status}</p>
      <button onClick={() => void login("admin", "pw").catch(() => {})}>
        sign in
      </button>
    </>
  );
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

describe("AuthProvider", () => {
  afterEach(async () => {
    vi.unstubAllGlobals();
    // rehydrate() shares one in-flight check at module scope and drops it a
    // tick after it settles; let that tick pass so tests do not share it.
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  // The page asks for the session on load. When that answer is slow and the
  // user signs in meanwhile, the late "no session" used to land after the
  // sign-in and send them back to /login.
  it("keeps a sign-in that finished before the load-time session check", async () => {
    let answerMe!: (r: Response) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string) => {
        if (url === "/api/auth/me") {
          return new Promise<Response>((resolve) => (answerMe = resolve));
        }
        if (url === "/api/auth/login") {
          return Promise.resolve(json(200, { user: USER, accessTokens: [] }));
        }
        return Promise.reject(new Error(`unexpected ${url}`));
      }),
    );
    render(
      <AuthProvider>
        <Status />
      </AuthProvider>,
    );

    await act(async () => {
      screen.getByRole("button", { name: "sign in" }).click();
    });
    await waitFor(() =>
      expect(screen.getByText("status: authenticated")).toBeInTheDocument(),
    );

    await act(async () => {
      answerMe(json(401, { error: "no session" }));
    });
    expect(screen.getByText("status: authenticated")).toBeInTheDocument();
  });

  it("adopts the session the load-time check finds", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(json(200, { user: USER, accessTokens: [] }))),
    );
    render(
      <AuthProvider>
        <Status />
      </AuthProvider>,
    );
    expect(
      await screen.findByText("status: authenticated"),
    ).toBeInTheDocument();
  });
});
