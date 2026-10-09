import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { setDisplayTimeZone } from "@/lib/format/locale";
import { NO_TIMESTAMP } from "@/lib/format/timestamp";

import { Timestamp } from "./Timestamp";

// 2026-10-01T04:21:15Z.
const AT = { seconds: 1790828475n };

describe("Timestamp", () => {
  afterEach(() => {
    setDisplayTimeZone(undefined);
  });

  it("shows the user's zone and the UTC instant on hover", () => {
    setDisplayTimeZone("Europe/Kyiv");
    render(<Timestamp ts={AT} />);
    const el = screen.getByText("2026-10-01 07:21:15 +03:00");
    expect(el.tagName).toBe("TIME");
    expect(el).toHaveAttribute("title", "2026-10-01 04:21:15Z");
  });

  it("writes an absent timestamp as no timestamp", () => {
    const { container } = render(<Timestamp ts={undefined} />);
    expect(container.textContent).toBe(NO_TIMESTAMP);
    expect(container.querySelector("time")).toBeNull();
  });
});
