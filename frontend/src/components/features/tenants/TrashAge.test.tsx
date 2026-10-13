import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  TrashRetention,
  retentionDays,
  retentionElapsed,
  trashAge,
  trashedAgo,
} from "./TrashAge";

const DAY = 24 * 60 * 60 * 1000;
const deleted = new Date("2026-09-01T12:00:00Z");
// What the server sends for a 30-day retention: deleted_at + 30d.
const eligible = new Date(deleted.getTime() + 30 * DAY);
const deletedAt = timestampFromDate(deleted);
const purgeEligibleAt = timestampFromDate(eligible);

describe("trashAge", () => {
  it("is null for a tenant that is not in the trash", () => {
    expect(trashAge(undefined, undefined, new Date())).toBeNull();
  });

  it("counts whole days in the trash", () => {
    const now = new Date(deleted.getTime() + 12 * DAY + 5 * 60 * 60 * 1000);
    const info = trashAge(deletedAt, purgeEligibleAt, now);
    expect(info?.days).toBe(12);
    expect(info?.pastRetention).toBe(false);
    expect(info?.daysLeft).toBe(18);
  });

  // The boundary is the server's purge_eligible_at, inclusive.
  it("turns past retention exactly at purge_eligible_at", () => {
    const before = trashAge(
      deletedAt,
      purgeEligibleAt,
      new Date(eligible.getTime() - 1),
    );
    expect(before?.pastRetention).toBe(false);
    expect(before?.daysLeft).toBe(1);

    const at = trashAge(deletedAt, purgeEligibleAt, eligible);
    expect(at?.pastRetention).toBe(true);
    expect(at?.daysLeft).toBeNull();
    expect(at?.days).toBe(30);
  });

  it("never marks a tenant when the server reported no retention", () => {
    const info = trashAge(
      deletedAt,
      undefined,
      new Date(deleted.getTime() + 400 * DAY),
    );
    expect(info?.pastRetention).toBe(false);
    expect(info?.daysLeft).toBeNull();
  });

  it("says how long ago, in days", () => {
    expect(trashedAgo(0)).toBe("today");
    expect(trashedAgo(1)).toBe("1 day ago");
    expect(trashedAgo(12)).toBe("12 days ago");
  });
});

describe("retention window", () => {
  const at = (days: number) => new Date(deleted.getTime() + days * DAY);
  const info = (now: Date) => trashAge(deletedAt, purgeEligibleAt, now)!;

  it("is the share of the window used, clamped to 0..1", () => {
    expect(retentionElapsed(info(at(0)), at(0))).toBe(0);
    expect(retentionElapsed(info(at(12)), at(12))).toBeCloseTo(0.4);
    expect(retentionElapsed(info(at(30)), at(30))).toBe(1);
    expect(retentionElapsed(info(at(45)), at(45))).toBe(1);
  });

  it("is the window's length in days", () => {
    expect(retentionDays(info(at(1)))).toBe(30);
  });

  it("is unknown when the server reported no retention", () => {
    const none = trashAge(deletedAt, undefined, at(1))!;
    expect(retentionElapsed(none, at(1))).toBeNull();
    expect(retentionDays(none)).toBeNull();
  });
});

describe("<TrashRetention>", () => {
  it("shows the days left and how much of the window is used", () => {
    render(
      <TrashRetention
        deletedAt={deletedAt}
        purgeEligibleAt={purgeEligibleAt}
        now={new Date(deleted.getTime() + 12 * DAY)}
      />,
    );
    expect(screen.getByText("18 days left")).toBeInTheDocument();
    expect(screen.getByRole("progressbar")).toHaveAttribute(
      "aria-valuenow",
      "40",
    );
    expect(screen.queryByText("Past retention")).not.toBeInTheDocument();
  });

  it("marks a tenant past retention with a full bar", () => {
    render(
      <TrashRetention
        deletedAt={deletedAt}
        purgeEligibleAt={purgeEligibleAt}
        now={new Date(deleted.getTime() + 45 * DAY)}
      />,
    );
    expect(screen.getByText("Past retention")).toBeInTheDocument();
    expect(screen.getByRole("progressbar")).toHaveAttribute(
      "aria-valuenow",
      "100",
    );
    expect(screen.queryByText(/left$/)).not.toBeInTheDocument();
  });

  it("says so when the server reported no retention", () => {
    render(
      <TrashRetention deletedAt={deletedAt} purgeEligibleAt={undefined} />,
    );
    expect(screen.getByText("No retention reported")).toBeInTheDocument();
  });
});
