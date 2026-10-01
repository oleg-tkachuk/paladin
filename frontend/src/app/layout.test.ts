import { describe, expect, it, vi } from "vitest";

vi.mock("geist/font/sans", () => ({ GeistSans: { variable: "" } }));
vi.mock("geist/font/mono", () => ({ GeistMono: { variable: "" } }));

import { metadata } from "./layout";

describe("root metadata", () => {
  // ClientLayout writes the title per route. One here as well would come
  // first in <head> and name every page "Paladin" again.
  it("leaves the title to the route", () => {
    expect(metadata.title).toBeUndefined();
  });
});
