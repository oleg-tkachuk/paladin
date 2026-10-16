import { describe, expect, it } from "vitest";

import { contentSecurityPolicy } from "./csp";

describe("contentSecurityPolicy", () => {
  it("holds every form to this origin", () => {
    const csp = contentSecurityPolicy({ isDev: false });
    expect(csp).toContain("form-action 'self'");
    expect(csp).toContain("upgrade-insecure-requests");
    expect(csp).not.toContain("'unsafe-eval'");
  });

  it("lets the consent form's redirect reach the client", () => {
    const csp = contentSecurityPolicy({ isDev: false, consent: true });
    expect(csp).not.toContain("form-action");
    // Everything else stays as strict.
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("frame-ancestors 'none'");
  });

  it("allows eval in dev only", () => {
    expect(contentSecurityPolicy({ isDev: true })).toContain("'unsafe-eval'");
    expect(contentSecurityPolicy({ isDev: true })).not.toContain(
      "upgrade-insecure-requests",
    );
  });
});
