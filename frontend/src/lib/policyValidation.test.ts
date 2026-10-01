import { describe, expect, it } from "vitest";

import { validateNotice } from "./policyValidation";

describe("validateNotice", () => {
  it("reports a clean policy as valid", () => {
    expect(validateNotice({ ok: true, diagnostics: [] })?.type).toBe("success");
  });

  it("does not call a policy with schema warnings valid", () => {
    const notice = validateNotice({
      ok: true,
      diagnostics: [{ severity: "warning" }, { severity: "warning" }],
    });
    expect(notice?.type).toBe("warning");
    expect(notice?.message).toMatch(/^2 schema findings/);
  });

  it("leaves a policy that does not compile to the diagnostics", () => {
    expect(
      validateNotice({ ok: false, diagnostics: [{ severity: "error" }] }),
    ).toBeNull();
  });
});
