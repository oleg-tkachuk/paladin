import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { validate } from "@cedar-policy/cedar-wasm/nodejs";

import { POLICY_TEMPLATES } from "./templates";

// The schema every stored policy is checked against on save. Cedar parses a
// policy that names an undeclared action or entity type and then matches
// nothing with it, so a template that only parses is a template that does
// nothing; the validator is what tells the two apart.
const SCHEMA = readFileSync(
  join(__dirname, "../../../../backend/policies/schema.cedarschema"),
  "utf8",
);

const STRICT = "strict" as const;

function validationErrors(cedar: string): string[] {
  const answer = validate({
    schema: SCHEMA,
    policies: { staticPolicies: cedar },
    validationSettings: { mode: STRICT },
  });
  if (answer.type === "failure") return answer.errors.map((e) => e.message);
  return answer.validationErrors.map((e) => e.error.message);
}

describe("policy templates", () => {
  it("have unique ids", () => {
    const ids = POLICY_TEMPLATES.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it.each(POLICY_TEMPLATES.map((t) => [t.id, t.cedar]))(
    "%s validates against the Cedar schema",
    (_id, cedar) => {
      expect(validationErrors(cedar)).toEqual([]);
    },
  );

  // Guards the gate itself: if the validator stopped seeing the schema, every
  // template would pass and this would too.
  it("the validator refuses an undeclared action", () => {
    expect(
      validationErrors(
        `permit (principal, action == Action::"ListObjects", resource);`,
      ),
    ).not.toEqual([]);
  });
});
