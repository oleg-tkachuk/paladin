import { describe, expect, it, vi } from "vitest";

vi.mock("geist/font/sans", () => ({ GeistSans: { variable: "" } }));
vi.mock("geist/font/mono", () => ({ GeistMono: { variable: "" } }));

import { metadata } from "./layout";

describe("root metadata", () => {
  // The rename turned "PALADIN — Paladin" into the same word twice.
  it("names the product once", () => {
    expect(metadata.title).toBe("Paladin");
  });
});
