import { act, render, screen, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { WhoAmIResponseSchema } from "@/gen/paladin/iam/v1/auth_service_pb";
import { UserSchema } from "@/gen/paladin/iam/v1/types_pb";
import { INVALID_CREDENTIALS_MESSAGE } from "@/lib/auth/loginFailure";
import { Code, ConnectError } from "@connectrpc/connect";
import { SESSION_RETRY_BASE_MS, type SessionCheck } from "@/lib/session/check";
import { SessionError } from "@/lib/session/client";
import { signalSessionEnded } from "@/lib/session/ended";

const session = vi.hoisted(() => ({
  refreshSession: vi.fn(),
  signIn: vi.fn(),
  signOut: vi.fn(),
  switchSessionTenant: vi.fn(),
}));
const whoAmI = vi.hoisted(() => vi.fn());

vi.mock("@/lib/session/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/session/client")>()),
  ...session,
}));
vi.mock("@/lib/connect/client", () => ({ authClient: { whoAmI } }));

import { AuthProvider, useAuth } from "./AuthContext";

const SIGNED_IN: SessionCheck = { kind: "signed-in" };
const SIGNED_OUT: SessionCheck = { kind: "signed-out" };
const UNAVAILABLE: SessionCheck = { kind: "unavailable", retryAfterMs: null };

const whoAmIAs = (subject: string, tenantSlug: string) =>
  create(WhoAmIResponseSchema, {
    tenantSlug,
    user: create(UserSchema, {
      userId: "u-1",
      tenantId: "t-1",
      subject,
      roles: ["platform.admin"],
    }),
  });

function Probe() {
  const { status, user, error, login, switchTenant, logout } = useAuth();
  return (
    <>
      <p>status: {status}</p>
      <p>user: {user ? `${user.subject}@${user.tenantSlug}` : "none"}</p>
      <p>error: {error ?? "none"}</p>
      <button onClick={() => void login("admin", "pw").catch(() => {})}>
        sign in
      </button>
      <button onClick={() => void switchTenant("t-2")}>switch</button>
      <button onClick={() => void logout()}>sign out</button>
    </>
  );
}

const renderProbe = () =>
  render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  );

const click = (name: string) =>
  act(async () => {
    screen.getByRole("button", { name }).click();
  });

describe("AuthProvider", () => {
  beforeEach(() => {
    for (const fn of Object.values(session)) fn.mockReset();
    whoAmI.mockReset();
  });

  afterEach(async () => {
    // rehydrate() shares one in-flight check at module scope and drops it a
    // tick after it settles; let that tick pass so tests do not share it.
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  it("adopts the session the load-time check finds, named by WhoAmI", async () => {
    session.refreshSession.mockResolvedValue(SIGNED_IN);
    whoAmI.mockResolvedValue(whoAmIAs("admin", "platform"));
    renderProbe();
    expect(
      await screen.findByText("status: authenticated"),
    ).toBeInTheDocument();
    expect(screen.getByText("user: admin@platform")).toBeInTheDocument();
  });

  it("asks nobody who the user is when there is no session", async () => {
    session.refreshSession.mockResolvedValue(SIGNED_OUT);
    renderProbe();
    expect(
      await screen.findByText("status: unauthenticated"),
    ).toBeInTheDocument();
    expect(whoAmI).not.toHaveBeenCalled();
  });

  // The page asks for the session on load. When that answer is slow and the
  // user signs in meanwhile, the late "no session" used to land after the
  // sign-in and send them back to /login.
  it("keeps a sign-in that finished before the load-time session check", async () => {
    let answer!: (found: SessionCheck) => void;
    session.refreshSession.mockReturnValue(
      new Promise<SessionCheck>((resolve) => (answer = resolve)),
    );
    session.signIn.mockResolvedValue(undefined);
    whoAmI.mockResolvedValue(whoAmIAs("admin", "platform"));
    renderProbe();

    await click("sign in");
    await waitFor(() =>
      expect(screen.getByText("status: authenticated")).toBeInTheDocument(),
    );
    await act(async () => answer(SIGNED_OUT));
    expect(screen.getByText("status: authenticated")).toBeInTheDocument();
  });

  it("says a refused password the way the form should", async () => {
    session.refreshSession.mockResolvedValue(SIGNED_OUT);
    session.signIn.mockRejectedValue(
      new SessionError("invalid credentials", "unauthenticated", 401),
    );
    renderProbe();
    await screen.findByText("status: unauthenticated");
    await click("sign in");
    expect(
      await screen.findByText(`error: ${INVALID_CREDENTIALS_MESSAGE}`),
    ).toBeInTheDocument();
  });

  it("reads the user again after a tenant switch", async () => {
    session.refreshSession.mockResolvedValue(SIGNED_IN);
    whoAmI
      .mockResolvedValueOnce(whoAmIAs("admin", "platform"))
      .mockResolvedValueOnce(whoAmIAs("admin", "acme"));
    session.switchSessionTenant.mockResolvedValue(undefined);
    renderProbe();
    await screen.findByText("user: admin@platform");
    await click("switch");
    expect(await screen.findByText("user: admin@acme")).toBeInTheDocument();
    expect(session.switchSessionTenant).toHaveBeenCalledWith("t-2");
  });

  it("signs out, and signs the page out when the session ends elsewhere", async () => {
    session.refreshSession.mockResolvedValue(SIGNED_IN);
    whoAmI.mockResolvedValue(whoAmIAs("admin", "platform"));
    session.signOut.mockResolvedValue(undefined);
    renderProbe();
    await screen.findByText("status: authenticated");
    await click("sign out");
    expect(screen.getByText("status: unauthenticated")).toBeInTheDocument();

    session.signIn.mockResolvedValue(undefined);
    await click("sign in");
    await screen.findByText("status: authenticated");
    await act(async () => signalSessionEnded());
    expect(screen.getByText("status: unauthenticated")).toBeInTheDocument();
    expect(screen.getByText("user: none")).toBeInTheDocument();
  });

  // The role could not check the session (503): the page keeps it, says so,
  // and asks again with backoff until the role answers.
  it("stays unavailable, not signed out, on a 503 and retries until the session is found", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      session.refreshSession
        .mockResolvedValueOnce(UNAVAILABLE)
        .mockResolvedValueOnce(UNAVAILABLE)
        .mockResolvedValue(SIGNED_IN);
      whoAmI.mockResolvedValue(whoAmIAs("admin", "platform"));
      renderProbe();
      expect(
        await screen.findByText("status: unavailable"),
      ).toBeInTheDocument();
      expect(session.refreshSession).toHaveBeenCalledTimes(1);

      await act(async () => vi.advanceTimersByTimeAsync(SESSION_RETRY_BASE_MS));
      expect(session.refreshSession).toHaveBeenCalledTimes(2);
      expect(screen.getByText("status: unavailable")).toBeInTheDocument();

      // The second wait is twice the first.
      await act(async () =>
        vi.advanceTimersByTimeAsync(2 * SESSION_RETRY_BASE_MS),
      );
      expect(
        await screen.findByText("status: authenticated"),
      ).toBeInTheDocument();
      expect(session.refreshSession).toHaveBeenCalledTimes(3);
    } finally {
      vi.useRealTimers();
    }
  });

  it("stays unavailable when WhoAmI cannot be answered, and signs out when it is refused", async () => {
    session.refreshSession.mockResolvedValue(SIGNED_IN);
    whoAmI.mockRejectedValue(new ConnectError("down", Code.Unavailable));
    const { unmount } = renderProbe();
    expect(await screen.findByText("status: unavailable")).toBeInTheDocument();
    unmount();
    await new Promise((resolve) => setTimeout(resolve, 0));

    whoAmI.mockRejectedValue(
      new ConnectError("no session", Code.Unauthenticated),
    );
    renderProbe();
    expect(
      await screen.findByText("status: unauthenticated"),
    ).toBeInTheDocument();
  });

  it("stops retrying once the user signs in", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      session.refreshSession.mockResolvedValue(UNAVAILABLE);
      session.signIn.mockResolvedValue(undefined);
      whoAmI.mockResolvedValue(whoAmIAs("admin", "platform"));
      renderProbe();
      await screen.findByText("status: unavailable");
      await click("sign in");
      await screen.findByText("status: authenticated");
      await act(async () => vi.advanceTimersByTimeAsync(SESSION_RETRY_BASE_MS));
      expect(session.refreshSession).toHaveBeenCalledTimes(1);
      expect(screen.getByText("status: authenticated")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
