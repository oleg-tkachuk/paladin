import { describe, expect, it } from "vitest";

import { BYTE_UNITS, BYTES_PER_STEP, bytesParts } from "./bytes";

describe("bytesParts", () => {
  it.each([
    [0, "0", "Bytes", "0 bytes"],
    [512, "512", "Bytes", "512 bytes"],
    [1536, "1.5", "KB", "1,536 bytes"],
    [1_048_576, "1", "MB", "1,048,576 bytes"],
    [1_073_741_824n, "1", "GB", "1,073,741,824 bytes"],
  ])("%s → %s %s (%s)", (bytes, value, unit, exact) => {
    expect(bytesParts(bytes)).toEqual({ value, unit, exact });
  });

  it("rounds the figure to the decimals asked for", () => {
    expect(bytesParts(1536, 0).value).toBe("2");
    expect(bytesParts(1_500_000, 1).value).toBe("1.4");
  });

  it("stays in the last unit past it", () => {
    const past = BYTES_PER_STEP ** BYTE_UNITS.length;
    expect(bytesParts(past).unit).toBe(BYTE_UNITS.at(-1));
    expect(bytesParts(past).value).toBe(String(BYTES_PER_STEP));
  });
});
