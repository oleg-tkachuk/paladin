import { describe, expect, it } from "vitest";

import {
  OAUTH_SCOPE_READ,
  OAUTH_SCOPE_WRITE,
  describeOAuthScope,
  grantsWrite,
} from "./oauthScopes";

describe("describeOAuthScope", () => {
  it("names what each known scope allows", () => {
    expect(describeOAuthScope(OAUTH_SCOPE_READ)).toMatch(/^Read/);
    expect(describeOAuthScope(OAUTH_SCOPE_WRITE)).toMatch(/delete/);
  });
  it("shows an unknown scope as itself", () => {
    expect(describeOAuthScope("custom.scope")).toBe("custom.scope");
  });
});

describe("grantsWrite", () => {
  it("is true only with the write scope", () => {
    expect(grantsWrite([OAUTH_SCOPE_READ, OAUTH_SCOPE_WRITE])).toBe(true);
    expect(grantsWrite([OAUTH_SCOPE_READ])).toBe(false);
    expect(grantsWrite([])).toBe(false);
  });
});
