import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { DEFAULT_ACTION } from "./_TestSuite";

// The schema the server resolves simulated actions against.
const SCHEMA = readFileSync(
  join(__dirname, "../../../../backend/policies/schema.cedarschema"),
  "utf8",
);

// Every name inside an `action "A", "B" appliesTo` declaration.
function declaredActions(schema: string): Set<string> {
  const names = new Set<string>();
  for (const decl of schema.matchAll(/^action\s+([^{]+?)\s+appliesTo/gms)) {
    for (const m of decl[1].matchAll(/"([^"]+)"/g)) names.add(m[1]);
  }
  return names;
}

describe("TestSuite default action", () => {
  it("is declared in the Cedar schema", () => {
    const declared = declaredActions(SCHEMA);
    expect(declared.size).toBeGreaterThan(0);
    expect(declared).toContain(DEFAULT_ACTION);
  });
});
