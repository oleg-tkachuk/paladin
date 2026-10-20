import { describe, expect, it } from "vitest";
import { render, screen } from "@/test/utils";

import { TokenUsage, type UsageSnap } from "./TokenRecordPanel";

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
