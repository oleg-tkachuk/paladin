// Money / unit formatting helpers.
//
// Mirrors the backend's capability.AllowedUnitCodes set
// (internal/capability/types.go). ISO 4217 fiat codes plus the
// abstract sentinel UNIT for non-currency metering. The backend
// validates writes; the frontend mirrors the list for type safety
// and to populate the Issue dialog's currency picker.

// Allowed unit codes. ISO 4217 fiat + UNIT sentinel for non-currency
// metering. Backend constrains writes; frontend mirrors for type
// safety on the consumer side.
import { DISPLAY_LOCALE } from "./locale";

export const ALLOWED_UNIT_CODES = ["USD", "EUR", "UAH", "GBP", "UNIT"] as const;

// isISOCurrency returns true when the unit_code is an ISO 4217
// currency the frontend knows how to format. UNIT and unknowns
// return false (caller should render plain "X.XX <code>").
export function isISOCurrency(code: string): boolean {
  // The five-element list is small; substring matches over indexOf
  // are fine. Using a Set would force the caller to import the
  // collection rather than the constant.
  return code === "USD" || code === "EUR" || code === "UAH" || code === "GBP";
}

// formatMoney renders an amount + unit_code as a localized string.
// ISO 4217 currencies use Intl.NumberFormat's currency style which
// emits the proper symbol and minor-unit handling per locale ($1.23,
// €1,23, ₴1,23). UNIT renders as plain "X.XX units" — no locale
// currency formatting (because no currency).
//
// `locale` defaults to the browser's preferred locale; pass undefined
// to get whatever Intl picks. Pass an explicit locale (e.g. "en-US",
// "uk-UA") when the caller wants deterministic output.
//
// `fractionDigits` lets callers ask for tighter / wider precision
// (the per-capability spend column uses 4 decimals so a $0.0001 LLM
// charge doesn't render as $0.00). Defaults: ISO currency uses the
// currency's standard minor units (Intl picks for us); UNIT defaults
// to 2.
export function formatMoney(
  amount: number,
  unitCode: string,
  locale: string = DISPLAY_LOCALE,
  fractionDigits?: number,
): string {
  if (!Number.isFinite(amount)) return "—";
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
      // Locale / currency-symbol resolution can throw on exotic
      // locale strings; fall through to the generic path so the UI
      // never renders "—" for a perfectly valid amount.
    }
  }
  // UNIT or unknown code — render the bare number plus the code as a
  // suffix. "1.23 UNIT" / "1.23 XYZ" reads as a quantity, never as
  // currency. We default fractionDigits to 2 so the number doesn't
  // render with bigint-style trailing zeroes.
  const digits = typeof fractionDigits === "number" ? fractionDigits : 2;
  const num = amount.toLocaleString(locale, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
  // UNIT renders as "1.23 units" (lowercased + plural for read-
  // ability); other unknown codes render with the code verbatim.
  if (unitCode === "UNIT") {
    return `${num} units`;
  }
  return `${num} ${unitCode || ""}`.trim();
}

// Money crosses the API as int64 micros — millionths of the unit, so
// 1.5 USD is 1_500_000n — which protobuf-es delivers as bigint.

const MICROS_PER_UNIT = 1_000_000n;

// MAX_MICROS mirrors capability.MaxMicros: fifteen significant digits,
// the most a JavaScript number carries exactly. The server refuses more.
export const MAX_MICROS = 999_999_999_999_999n;

// fromMicros turns micros into a number for display and arithmetic on the
// screen. Exact for anything up to MAX_MICROS.
export function fromMicros(micros: bigint | undefined): number {
  return micros === undefined ? 0 : Number(micros) / 1_000_000;
}

// parseMicros reads what an operator typed — "25", "19.99", ".5" — into
// micros without going through a float, so "0.1" is exactly 100_000n.
// null for anything that is not a non-negative decimal with at most six
// fractional digits, or that exceeds MAX_MICROS.
export function parseMicros(text: string): bigint | null {
  const match = /^(\d*)(?:\.(\d*))?$/.exec(text.trim());
  if (!match) return null;
  const [, whole = "", frac = ""] = match;
  if (whole === "" && frac === "") return null;
  if (frac.length > 6) return null;
  const micros =
    BigInt(whole || "0") * MICROS_PER_UNIT + BigInt(frac.padEnd(6, "0"));
  return micros > MAX_MICROS ? null : micros;
}

// microsToInput renders micros as the shortest exact decimal, for seeding a
// form field: 100_000_000n → "100", 1_500_000n → "1.5".
export function microsToInput(micros: bigint): string {
  const whole = micros / MICROS_PER_UNIT;
  const frac = (micros % MICROS_PER_UNIT)
    .toString()
    .padStart(6, "0")
    .replace(/0+$/, "");
  return frac ? `${whole}.${frac}` : `${whole}`;
}
