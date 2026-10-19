import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { PRINCIPAL_KINDS } from "./principalKind";

// Where the backend names the kinds Cedar sees.
const SOURCE = readFileSync(
  join(__dirname, "../../../backend/internal/auth/context.go"),
  "utf8",
);

describe("PRINCIPAL_KINDS", () => {
  it("are exactly the wire names the backend gives principal.kind", () => {
    const backend = [
      ...SOURCE.matchAll(/PrincipalKindName\w+\s*=\s*"([^"]+)"/g),
    ].map((m) => m[1]);
    expect(backend.length).toBeGreaterThan(0);
    expect(PRINCIPAL_KINDS.map((k) => k.value).sort()).toEqual(backend.sort());
  });
});
