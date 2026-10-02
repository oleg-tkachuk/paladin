import { beforeEach, describe, expect, it, vi } from "vitest";

const readSessionCookie = vi.fn<() => Promise<string | null>>();
const exchangeAudience = vi.fn();

vi.mock("./bff", () => ({
  readSessionCookie: () => readSessionCookie(),
  iamAuthClient: () => ({ exchangeAudience }),
}));

import { AUDIENCES } from "@/constants";

import { requireSession } from "./session";

describe("requireSession", () => {
  beforeEach(() => {
    readSessionCookie.mockReset();
    exchangeAudience.mockReset();
  });

  it("refuses a caller with no session cookie, without asking IAM", async () => {
    readSessionCookie.mockResolvedValue(null);
    const res = await requireSession();
    expect(res?.status).toBe(401);
    expect(exchangeAudience).not.toHaveBeenCalled();
  });

  it("refuses a cookie IAM will not exchange", async () => {
    readSessionCookie.mockResolvedValue("forged");
    exchangeAudience.mockRejectedValue(new Error("unauthenticated"));
    const res = await requireSession();
    expect(res?.status).toBe(401);
  });

  it("lets a live session through, proven against the iam audience", async () => {
    readSessionCookie.mockResolvedValue("live-refresh-token");
    exchangeAudience.mockResolvedValue({ accessToken: "at" });
    expect(await requireSession()).toBeNull();
    expect(exchangeAudience).toHaveBeenCalledWith({
      refreshToken: "live-refresh-token",
      targetAudience: AUDIENCES.iam,
    });
  });
});
