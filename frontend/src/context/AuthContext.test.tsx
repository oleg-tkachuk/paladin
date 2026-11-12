import { act, render, screen, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { WhoAmIResponseSchema } from "@/gen/paladin/iam/v1/auth_service_pb";
import { UserSchema } from "@/gen/paladin/iam/v1/types_pb";
import { INVALID_CREDENTIALS_MESSAGE } from "@/lib/auth/loginFailure";
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
    session.refreshSession.mockResolvedValue(true);
    whoAmI.mockResolvedValue(whoAmIAs("admin", "platform"));
    renderProbe();
    expect(
      await screen.findByText("status: authenticated"),
    ).toBeInTheDocument();
    expect(screen.getByText("user: admin@platform")).toBeInTheDocument();
  });

  it("asks nobody who the user is when there is no session", async () => {
    session.refreshSession.mockResolvedValue(false);
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
    let answer!: (found: boolean) => void;
    session.refreshSession.mockReturnValue(
      new Promise<boolean>((resolve) => (answer = resolve)),
    );
    session.signIn.mockResolvedValue(undefined);
    whoAmI.mockResolvedValue(whoAmIAs("admin", "platform"));
    renderProbe();

    await click("sign in");
    await waitFor(() =>
      expect(screen.getByText("status: authenticated")).toBeInTheDocument(),
    );
    await act(async () => answer(false));
    expect(screen.getByText("status: authenticated")).toBeInTheDocument();
  });

  it("says a refused password the way the form should", async () => {
    session.refreshSession.mockResolvedValue(false);
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
    session.refreshSession.mockResolvedValue(true);
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
    session.refreshSession.mockResolvedValue(true);
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
});
