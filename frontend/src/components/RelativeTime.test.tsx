import { describe, expect, it } from "vitest";

import { render, screen } from "@/test/utils";
import { formatDateTime, formatInstant } from "@/lib/format/locale";

import { RelativeTime, exactTime } from "./RelativeTime";

const AT = new Date("2026-10-01T13:18:33Z");
const ts = { seconds: BigInt(AT.getTime() / 1000), nanos: 0 };

describe("RelativeTime", () => {
  // The tooltip read 2026-10-01T13:18:33.000Z, which no one reads at a glance.
  it("shows the date a relative time stands for on hover", () => {
    render(<RelativeTime ts={ts} />);
    const time = screen.getByRole("time");
    expect(time).toHaveAttribute("title", formatDateTime(AT));
    expect(time.getAttribute("title")).not.toMatch(/T\\d\\d:/);
    expect(time).toHaveAttribute("dateTime", AT.toISOString());
  });

  it("shows the instant with its offset under an absolute date", () => {
    render(<RelativeTime ts={ts} absolute />);
    expect(screen.getByRole("time")).toHaveAttribute(
      "title",
      formatInstant(AT),
    );
  });

  it("says there is no time when there is none", () => {
    render(<RelativeTime ts={undefined} />);
    expect(screen.getByTitle("No timestamp")).toHaveTextContent("—");
  });
});

describe("exactTime", () => {
  it("is the console's date for a relative time, the instant for an absolute one", () => {
    expect(exactTime(AT, false)).toBe(formatDateTime(AT));
    expect(exactTime(AT, true)).toBe(formatInstant(AT));
  });
});
