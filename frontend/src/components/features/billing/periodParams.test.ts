import { describe, expect, it } from "vitest";

import { DEFAULT_PRESET } from "./period";
import {
  FROM_PARAM,
  PERIOD_PARAM,
  TO_PARAM,
  periodFromParams,
  querySuffix,
  withPeriod,
} from "./periodParams";

describe("billing period params", () => {
  it("reads a preset, a custom span, and falls back to the default", () => {
    expect(periodFromParams(new URLSearchParams("period=7d"))).toEqual({
      preset: "7d",
      from: "",
      to: "",
    });
    expect(
      periodFromParams(
        new URLSearchParams("period=custom&from=2026-10-01&to=2026-10-15"),
      ),
    ).toEqual({ preset: "custom", from: "2026-10-01", to: "2026-10-15" });
    expect(periodFromParams(new URLSearchParams("period=bogus")).preset).toBe(
      DEFAULT_PRESET,
    );
    // Dates count only for a custom period.
    expect(periodFromParams(new URLSearchParams("from=2026-10-01")).from).toBe(
      "",
    );
  });

  it("writes a period beside the other params, the default left out", () => {
    const base = new URLSearchParams("tab=x&period=90d");
    expect(
      withPeriod(base, { preset: DEFAULT_PRESET, from: "", to: "" }).toString(),
    ).toBe("tab=x");
    const custom = withPeriod(base, {
      preset: "custom",
      from: "2026-10-01",
      to: "",
    });
    expect(custom.get(PERIOD_PARAM)).toBe("custom");
    expect(custom.get(FROM_PARAM)).toBe("2026-10-01");
    expect(custom.has(TO_PARAM)).toBe(false);
    expect(custom.get("tab")).toBe("x");
  });

  it("reads back what it writes", () => {
    const choice = {
      preset: "custom" as const,
      from: "2026-09-01",
      to: "2026-09-30",
    };
    expect(periodFromParams(withPeriod(new URLSearchParams(), choice))).toEqual(
      choice,
    );
  });

  it("suffixes a query only when there is one", () => {
    expect(querySuffix(new URLSearchParams())).toBe("");
    expect(querySuffix(new URLSearchParams("period=7d"))).toBe("?period=7d");
  });
});
