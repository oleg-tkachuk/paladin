import { describe, expect, it } from "vitest";

import { USERS_INDEX_HREF, userHref, userResourceName } from "./userPath";

describe("userPath", () => {
  it("maps a user's resource name to their page and back", () => {
    expect(userHref("tenants/t-1/users/u-1")).toBe("/users/t-1/u-1");
    expect(userResourceName("t-1", "u-1")).toBe("tenants/t-1/users/u-1");
  });

  it("escapes what a path segment cannot hold", () => {
    expect(userHref("tenants/a b/users/c?d")).toBe("/users/a%20b/c%3Fd");
  });

  it("falls back to the index for a name that is not a user's", () => {
    expect(userHref("users/u-1")).toBe(USERS_INDEX_HREF);
    expect(userHref("tenants/t-1/users/u-1/extra")).toBe(USERS_INDEX_HREF);
  });
});
