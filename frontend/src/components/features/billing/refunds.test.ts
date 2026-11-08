import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { MoneySchema } from "@/gen/google/type/money_pb";

import { refundedNote } from "./refunds";

const SPEND_DIGITS = 4;

describe("refundedNote", () => {
  it("names the amount refunded", () => {
    const refunded = create(MoneySchema, {
      currencyCode: "USD",
      nanos: 250_000_000,
    });
    // The symbol is the display locale's; the amount and the word are ours.
    expect(refundedNote(refunded, { fractionDigits: SPEND_DIGITS })).toMatch(
      /\$0\.2500 refunded$/,
    );
  });

  it("says nothing when nothing was refunded", () => {
    expect(refundedNote(undefined)).toBeUndefined();
    expect(
      refundedNote(create(MoneySchema, { currencyCode: "USD" })),
    ).toBeUndefined();
  });

  it("takes the fallback unit for an amount that names none", () => {
    const refunded = create(MoneySchema, { units: BigInt(2) });
    expect(refundedNote(refunded, { fallbackUnit: "EUR" })).toMatch(
      /€2\.00 refunded$/,
    );
  });
});
