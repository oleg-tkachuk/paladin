import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  updateUser: vi.fn(),
  deleteUser: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  userClient: { updateUser: h.updateUser, deleteUser: h.deleteUser },
}));
vi.mock("@/context/RefreshContext", () => ({ useBumpRefresh: () => vi.fn() }));

import type { User } from "@/gen/paladin/iam/v1/types_pb";
import { USERS_LIST_QUERY_KEY, userDetailQuery } from "@/lib/queries/details";

import {
  useUserAdmin,
  userChangeEdits,
  userRemovalEdits,
} from "./useUserAdmin";

const ADA = "tenants/t-1/users/ada";
const BOB = "tenants/t-1/users/bob";
const user = (name: string, extra = {}) => ({
  name,
  displayName: name.split("/").at(-1),
  disabled: false,
  roles: ["tenant.user"],
  resourceVersion: "v1",
  ...extra,
});
const DETAIL = userDetailQuery(ADA).queryKey;

function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(USERS_LIST_QUERY_KEY, [user(ADA), user(BOB)]);
  qc.setQueryData(DETAIL, user(ADA) as unknown as User);
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(() => useUserAdmin(), { wrapper });
  const list = () =>
    qc.getQueryData<ReturnType<typeof user>[]>(USERS_LIST_QUERY_KEY);
  const detail = () => qc.getQueryData<ReturnType<typeof user>>(DETAIL);
  return { result, list, detail };
}

/** A server call the test answers when it chooses. */
function held() {
  let settle!: { resolve: (v: unknown) => void; reject: (e: Error) => void };
  const call = vi.fn(
    () => new Promise((resolve, reject) => (settle = { resolve, reject })),
  );
  return { call, settle: () => settle };
}

beforeEach(() => {
  h.updateUser.mockReset();
  h.deleteUser.mockReset();
});

describe("userChangeEdits", () => {
  it("sets only the fields given, on that user, in the list and its page", () => {
    const [list, page] = userChangeEdits(ADA, { disabled: true });
    expect(list.apply([user(ADA), user(BOB)])).toEqual([
      user(ADA, { disabled: true }),
      user(BOB),
    ]);
    expect(page.apply(user(ADA))).toEqual(user(ADA, { disabled: true }));
    expect(page.queryKey).toEqual(DETAIL);
  });

  it("changes nothing for a field left out", () => {
    const [list] = userChangeEdits(ADA, { displayName: undefined });
    expect(list.apply([user(ADA)])).toEqual([user(ADA)]);
  });
});

describe("userRemovalEdits", () => {
  it("takes the user out of the list", () => {
    const [list] = userRemovalEdits(ADA);
    expect(list.apply([user(ADA), user(BOB)])).toEqual([user(BOB)]);
  });
});

describe("useUserAdmin optimistic changes", () => {
  it("shows a disable before the answer, in the list and the user's page", async () => {
    const server = held();
    h.updateUser.mockImplementation(server.call);
    const { result, list, detail } = setup();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current.updateUser({
        name: ADA,
        resourceVersion: "v1",
        disabled: true,
      });
    });
    await vi.waitFor(() => expect(list()?.[0].disabled).toBe(true));
    expect(detail()?.disabled).toBe(true);
    expect(list()?.[1].disabled).toBe(false);
    await act(async () => {
      server.settle().resolve({});
      await done;
    });
  });

  it("puts a refused disable back, and says why", async () => {
    h.updateUser.mockRejectedValue(new Error("stale resource version"));
    const { result, list, detail } = setup();
    let res: Awaited<ReturnType<typeof result.current.updateUser>>;
    await act(async () => {
      res = await result.current.updateUser({
        name: ADA,
        resourceVersion: "v1",
        disabled: true,
      });
    });
    expect(res!).toEqual({ ok: false, error: "stale resource version" });
    expect(list()?.[0].disabled).toBe(false);
    expect(detail()?.disabled).toBe(false);
  });

  it("takes a deleted user out at once, and back when refused", async () => {
    const server = held();
    h.deleteUser.mockImplementation(server.call);
    const { result, list } = setup();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current.deleteUser({ name: ADA, resourceVersion: "v1" });
    });
    await vi.waitFor(() => expect(list()?.map((u) => u.name)).toEqual([BOB]));
    await act(async () => {
      server.settle().reject(new Error("denied"));
      await done;
    });
    expect(list()?.map((u) => u.name)).toEqual([ADA, BOB]);
  });
});
