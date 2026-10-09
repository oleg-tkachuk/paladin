// Money / unit formatting helpers.
//
// Every amount crosses the API as google.type.Money: whole `units` as a
// bigint plus `nanos`, billionths of the unit. The helpers here keep that
// exact — parsing, comparing and summing in bigint nanos — and convert to a
// JavaScript number only at the display edge (ratios, chart points).
//
// Mirrors limes.AllowedUnitCodes, the set the backend accepts
// (github.com/oleg-tkachuk/limes, types.go): four ISO 4217 fiat codes plus XXX, ISO 4217's code
// for "no currency", for metering that is not money. The backend validates
// writes; the frontend mirrors the list to populate the unit pickers.
import { create } from "@bufbuild/protobuf";

import { MoneySchema, type Money } from "@/gen/google/type/money_pb";
import { DISPLAY_LOCALE } from "./locale";

// ABSTRACT_UNIT_CODE is the unit of a budget that is not money, mirroring
// capability.AbstractUnitCode. It is also the console's fallback wherever an
// amount arrives without a unit.
export const ABSTRACT_UNIT_CODE = "XXX";

export const ALLOWED_UNIT_CODES = [
  "USD",
  "EUR",
  "UAH",
  "GBP",
  ABSTRACT_UNIT_CODE,
] as const;

// isISOCurrency returns true when the code is a currency the console renders
// with a currency symbol. XXX is a valid ISO 4217 code, but Intl renders it
// with the generic "¤" sign; it and unknown codes return false so the caller
// renders a plain "X.XX <unit>" instead.
export function isISOCurrency(code: string): boolean {
  return code === "USD" || code === "EUR" || code === "UAH" || code === "GBP";
}

// NANOS_PER_UNIT is how many nanos make one unit, mirroring
// limes.NanosPerUnit; NANOS_DECIMALS is the decimal places that carries.
export const NANOS_PER_UNIT = 1_000_000_000n;
export const NANOS_DECIMALS = 9;

// MAX_UNITS mirrors capability.MaxNanos / NanosPerUnit: the whole units of the
// largest amount the server counts. It refuses more.
export const MAX_UNITS = 999_999_999n;

// ABSTRACT_UNIT_LABEL is how an XXX amount reads: "1.50 units".
const ABSTRACT_UNIT_LABEL = "units";

// DEFAULT_ABSTRACT_DIGITS is the fraction digits a non-currency amount shows
// when the caller does not ask for a count.
const DEFAULT_ABSTRACT_DIGITS = 2;

// PERCENT is the scale of a utilisation ratio.
const PERCENT = 100;

// DECIMAL_INPUT is what an operator may type: digits, an optional point and
// optional fraction digits. No sign, no exponent, no grouping.
const DECIMAL_INPUT = /^(\d*)(?:\.(\d*))?$/;

// moneyFromDecimal reads what an operator typed — "25", "19.99", ".5" — into
// Money exactly, without going through a float: "0.1" is 0 units and
// 100_000_000 nanos. null for anything that is not a non-negative decimal
// with at most nine fractional digits, or that exceeds MAX_UNITS.
export function moneyFromDecimal(
  text: string,
  currencyCode: string,
): Money | null {
  const match = DECIMAL_INPUT.exec(text.trim());
  if (!match) return null;
  const [, whole = "", frac = ""] = match;
  if (whole === "" && frac === "") return null;
  if (frac.length > NANOS_DECIMALS) return null;
  const units = BigInt(whole || "0");
  if (units > MAX_UNITS) return null;
  return create(MoneySchema, {
    currencyCode,
    units,
    nanos: Number(frac.padEnd(NANOS_DECIMALS, "0")),
  });
}

// moneyToNanos is the amount in nanos, exact, for comparing and summing.
// An absent amount is zero.
export function moneyToNanos(m: Money | undefined): bigint {
  if (!m) return 0n;
  return m.units * NANOS_PER_UNIT + BigInt(m.nanos);
}

// moneyFromNanos builds Money from an exact nanos amount.
export function moneyFromNanos(nanos: bigint, currencyCode: string): Money {
  return create(MoneySchema, {
    currencyCode,
    units: nanos / NANOS_PER_UNIT,
    nanos: Number(nanos % NANOS_PER_UNIT),
  });
}

// moneyToDecimal renders the amount as the shortest exact decimal, for
// seeding a form field and for formatting: 100 units → "100", 1 unit and
// 500_000_000 nanos → "1.5". An absent amount is "0".
export function moneyToDecimal(m: Money | undefined): string {
  const total = moneyToNanos(m);
  const sign = total < 0n ? "-" : "";
  const abs = total < 0n ? -total : total;
  const whole = abs / NANOS_PER_UNIT;
  const frac = (abs % NANOS_PER_UNIT)
    .toString()
    .padStart(NANOS_DECIMALS, "0")
    .replace(/0+$/, "");
  return frac ? `${sign}${whole}.${frac}` : `${sign}${whole}`;
}

// moneyToNumber converts for the display edge only — a chart point, a
// ratio. Never compare or sum the result; use moneyToNanos.
export function moneyToNumber(m: Money | undefined): number {
  return Number(moneyToDecimal(m));
}

// moneyIsPositive reports a non-zero, non-negative amount: a budget that is
// set, as opposed to zero or absent, which mean "no budget".
export function moneyIsPositive(m: Money | undefined): boolean {
  return moneyToNanos(m) > 0n;
}

// moneyPercent is `part` as a percentage of `whole`, or null when `whole` is
// no budget. Computed from exact nanos; the number is for display only.
export function moneyPercent(
  part: Money | undefined,
  whole: Money | undefined,
): number | null {
  const denominator = moneyToNanos(whole);
  if (denominator <= 0n) return null;
  return (Number(moneyToNanos(part)) / Number(denominator)) * PERCENT;
}

// unitOf is the unit an amount is in, or XXX when it carries none.
export function unitOf(m: Money | undefined): string {
  return m?.currencyCode || ABSTRACT_UNIT_CODE;
}

// formatMoney renders Money as a localized string, exact to the digits it
// shows. ISO 4217 currencies use Intl.NumberFormat's currency style, which
// emits the symbol and minor units per locale ($1.23, €1.23, ₴1.23). XXX
// renders as plain "1.23 units", and an unknown code as "1.23 <code>".
//
// `fallbackUnit` is the unit of an amount that arrives without one; it
// defaults to XXX. `fractionDigits` asks for a fixed precision (the spend
// columns use 4 so a $0.0001 charge does not render as $0.00); by default a
// currency uses its standard minor units and anything else 2.
export function formatMoney(
  m: Money | undefined,
  {
    fallbackUnit = ABSTRACT_UNIT_CODE,
    fractionDigits,
    locale = DISPLAY_LOCALE,
  }: { fallbackUnit?: string; fractionDigits?: number; locale?: string } = {},
): string {
  const unitCode = m?.currencyCode || fallbackUnit;
  // A decimal string, not a number: Intl formats it exactly, where a float
  // would lose digits past fifteen significant.
  const amount = moneyToDecimal(m) as Intl.StringNumericLiteral;
  if (isISOCurrency(unitCode)) {
    const opts: Intl.NumberFormatOptions = {
      style: "currency",
      currency: unitCode,
    };
    if (typeof fractionDigits === "number") {
      opts.minimumFractionDigits = fractionDigits;
      opts.maximumFractionDigits = fractionDigits;
    }
    try {
      return new Intl.NumberFormat(locale, opts).format(amount);
    } catch {
      // Locale / currency-symbol resolution can throw on exotic locale
      // strings; fall through to the generic path so the UI never renders
      // nothing for a valid amount.
    }
  }
  const digits =
    typeof fractionDigits === "number"
      ? fractionDigits
      : DEFAULT_ABSTRACT_DIGITS;
  const num = new Intl.NumberFormat(locale, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(amount);
  if (unitCode === ABSTRACT_UNIT_CODE) {
    return `${num} ${ABSTRACT_UNIT_LABEL}`;
  }
  return `${num} ${unitCode}`.trim();
}
