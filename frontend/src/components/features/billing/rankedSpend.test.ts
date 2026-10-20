import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { TopEntrySchema } from "@/gen/paladin/admin/v1/billing_service_pb";
import { moneyFromNanos } from "@/lib/format/money";

import { RANKED_VISIBLE, rankedRows } from "./rankedSpend";

const entry = (label: string, nanos: bigint) =>
  create(TopEntrySchema, {
    label,
    spent: moneyFromNanos(nanos, "USD"),
    chargeCount: 1n,
  });

describe("rankedRows", () => {
  const entries = Array.from({ length: RANKED_VISIBLE + 2 }, (_, i) =>
    entry(`e${i}`, 10n),
  );

  it("shows the first few until opened, then every entry", () => {
    expect(rankedRows(entries, 100n, false)).toHaveLength(RANKED_VISIBLE);
    expect(rankedRows(entries, 100n, true)).toHaveLength(entries.length);
  });

  it("gives each its share of the whole period's spend", () => {
    const [half] = rankedRows([entry("a", 50n)], 100n, false);
    expect(half.sharePercent).toBe(50);
    expect(rankedRows([entry("a", 50n)], 0n, false)[0].sharePercent).toBe(0);
    // Never past the whole, should an entry outrun a stale total.
    expect(rankedRows([entry("a", 150n)], 100n, false)[0].sharePercent).toBe(
      100,
    );
  });
});
