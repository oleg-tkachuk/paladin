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

  it("lets the browser reach the storage origins it is given, and no other", () => {
    const strict = contentSecurityPolicy({ isDev: false });
    expect(strict).toContain("connect-src 'self';");
    expect(strict).toContain("img-src 'self' data: blob:;");

    const csp = contentSecurityPolicy({
      isDev: false,
      storageOrigins: ["https://s3.example.com", "http://localhost:9000"],
    });
    expect(csp).toContain(
      "connect-src 'self' https://s3.example.com http://localhost:9000;",
    );
    expect(csp).toContain(
      "img-src 'self' data: blob: https://s3.example.com http://localhost:9000;",
    );
    // Nothing else opens: scripts and frames stay on this origin.
    expect(csp).toContain("default-src 'self';");
    expect(csp).not.toMatch(/script-src[^;]*s3\.example/);
  });
});
