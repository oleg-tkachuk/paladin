import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { TrashAge, trashAge, trashAgeLabel } from "./TrashAge";

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

  it("labels the age in days", () => {
    expect(trashAgeLabel(0)).toBe("In trash less than a day");
    expect(trashAgeLabel(1)).toBe("In trash 1 day");
    expect(trashAgeLabel(12)).toBe("In trash 12 days");
  });
});

describe("<TrashAge>", () => {
  it("shows the age and the days left while within retention", () => {
    render(
      <TrashAge
        deletedAt={deletedAt}
        purgeEligibleAt={purgeEligibleAt}
        now={new Date(deleted.getTime() + 12 * DAY)}
      />,
    );
    expect(screen.getByText("In trash 12 days")).toBeInTheDocument();
    expect(screen.getByText("Purge-eligible in 18 days")).toBeInTheDocument();
    expect(screen.queryByText("Past retention")).not.toBeInTheDocument();
  });

  it("marks a tenant past retention", () => {
    render(
      <TrashAge
        deletedAt={deletedAt}
        purgeEligibleAt={purgeEligibleAt}
        now={new Date(deleted.getTime() + 45 * DAY)}
      />,
    );
    expect(screen.getByText("In trash 45 days")).toBeInTheDocument();
    const badge = screen.getByText("Past retention");
    expect(badge).toHaveAttribute(
      "title",
      expect.stringContaining("Nothing is purged automatically"),
    );
    expect(screen.queryByText(/Purge-eligible in/)).not.toBeInTheDocument();
  });

  it("renders a dash without a deletion time", () => {
    render(<TrashAge deletedAt={undefined} purgeEligibleAt={undefined} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});
