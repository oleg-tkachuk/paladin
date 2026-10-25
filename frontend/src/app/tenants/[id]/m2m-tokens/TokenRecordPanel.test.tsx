import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@/test/utils";
import { create } from "@bufbuild/protobuf";
import { TimestampSchema } from "@bufbuild/protobuf/wkt";

import {
  APITokenSchema,
  type APIToken,
} from "@/gen/paladin/admin/v1/api_token_service_pb";

import {
  TokenRecordPanel,
  TokenUsage,
  type UsageSnap,
} from "./TokenRecordPanel";

const READ_AT_MS = 1_700_000_000_000;
const snap = (over: Partial<UsageSnap>): UsageSnap => ({
  limitRpm: 100,
  weighted: 10,
  currentBucket: 0n,
  resetsAtMs: READ_AT_MS + 30_000,
  ...over,
});

describe("TokenUsage", () => {
  it.each([
    ["a revoked token", true, snap({}), "—"],
    ["a reading still loading", false, undefined, "…"],
    ["a token never verified", false, "never" as const, "never used"],
  ])("shows %s as such", (_, revoked, usage, text) => {
    render(
      <TokenUsage revoked={revoked} usage={usage} readAtMs={READ_AT_MS} />,
    );
    expect(screen.getByText(text)).toBeInTheDocument();
  });

  it("counts the reset down from when the usage was read", () => {
    render(
      <TokenUsage revoked={false} usage={snap({})} readAtMs={READ_AT_MS} />,
    );
    expect(screen.getByText("resets in 30s")).toBeInTheDocument();
    expect(screen.getByText("10.0")).not.toHaveClass("text-warning");
  });

  it("warns near the cap, and at once on a cap of 0", () => {
    const { unmount } = render(
      <TokenUsage
        revoked={false}
        usage={snap({ weighted: 90 })}
        readAtMs={READ_AT_MS}
      />,
    );
    expect(screen.getByText("90.0")).toHaveClass("text-warning");
    unmount();
    render(
      <TokenUsage
        revoked={false}
        usage={snap({ weighted: 0, limitRpm: 0 })}
        readAtMs={READ_AT_MS}
      />,
    );
    expect(screen.getByText("0.0")).toHaveClass("text-warning");
  });

  it("reads an uncapped token against infinity and never counts past zero", () => {
    render(
      <TokenUsage
        revoked={false}
        usage={snap({ limitRpm: undefined, resetsAtMs: READ_AT_MS - 5_000 })}
        readAtMs={READ_AT_MS}
      />,
    );
    expect(screen.getByText("/ ∞", { exact: false })).toBeInTheDocument();
    expect(screen.getByText("resets in 0s")).toBeInTheDocument();
  });
});

describe("TokenRecordPanel lifetime", () => {
  const CREATED_S = 1_700_000_000;
  const HOUR_S = 3_600;
  const DAY_S = 24 * HOUR_S;
  const LIFETIME_S = 30 * DAY_S;
  const REVOKED_AFTER_S = 2 * DAY_S + 3 * HOUR_S;
  const at = (seconds: number) =>
    create(TimestampSchema, { seconds: BigInt(seconds) });

  const openToken = (
    over: Partial<Pick<APIToken, "expiresAt" | "revokedAt">>,
  ) => {
    render(
      <TokenRecordPanel
        token={create(APITokenSchema, {
          id: "tok-1",
          displayName: "ci",
          createdAt: at(CREATED_S),
          ...over,
        })}
        usage="never"
        readAtMs={READ_AT_MS}
        onClose={vi.fn()}
        onRevoke={vi.fn()}
      />,
    );
    const panel = screen.getByRole("dialog", { name: "ci" });
    return (label: string) =>
      within(panel).queryByText(label)?.nextElementSibling?.textContent;
  };

  it.each([
    ["an expiry", { expiresAt: at(CREATED_S + LIFETIME_S) }, "30 d"],
    ["no expiry", {}, "no expiry"],
  ])("reads its validity with %s", (_, over, want) => {
    expect(openToken(over)("Valid for")).toBe(want);
  });

  it("says how long a revoked token lived", () => {
    const field = openToken({ revokedAt: at(CREATED_S + REVOKED_AFTER_S) });
    expect(field("Revoked after")).toBe("2 d 3 h");
  });

  it("names no revocation span for a live token", () => {
    expect(openToken({})("Revoked after")).toBeUndefined();
  });
});
