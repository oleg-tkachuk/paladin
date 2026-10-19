// A size as the console shows it: a short figure and its unit, with the
// exact count of bytes for a tooltip. Binary steps (1024), named as the
// console always has named them.
import { formatCount } from "./locale";

/** One step of the scale. */
export const BYTES_PER_STEP = 1024;
/** The units, one per step; a size past the last stays in it. */
export const BYTE_UNITS = [
  "Bytes",
  "KB",
  "MB",
  "GB",
  "TB",
  "PB",
  "EB",
] as const;
/** Decimals the short figure keeps unless asked for others. */
export const BYTE_DECIMALS = 2;

export interface ByteParts {
  /** The short figure: "1.5". */
  value: string;
  /** Its unit: "KB". */
  unit: (typeof BYTE_UNITS)[number];
  /** Every byte, counted: "1,536 bytes". */
  exact: string;
}

export function bytesParts(
  bytes: number | bigint,
  decimals: number = BYTE_DECIMALS,
): ByteParts {
  const n = typeof bytes === "bigint" ? Number(bytes) : bytes;
  const exact = `${formatCount(bytes)} bytes`;
  if (n <= 0) return { value: "0", unit: BYTE_UNITS[0], exact };
  const step = Math.min(
    Math.floor(Math.log(n) / Math.log(BYTES_PER_STEP)),
    BYTE_UNITS.length - 1,
  );
  const value = parseFloat(
    (n / BYTES_PER_STEP ** step).toFixed(Math.max(decimals, 0)),
  ).toString();
  return { value, unit: BYTE_UNITS[step], exact };
}
