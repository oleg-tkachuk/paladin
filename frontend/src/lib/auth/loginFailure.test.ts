import { describe, expect, it } from "vitest";

import { SessionError } from "@/lib/session/client";

import {
  INVALID_CREDENTIALS_MESSAGE,
  SIGN_IN_UNAVAILABLE_MESSAGE,
  loginFailure,
} from "./loginFailure";

describe("loginFailure", () => {
  it("says a refused password plainly", () => {
    expect(
      loginFailure(
        new SessionError("invalid credentials", "unauthenticated", 401),
      ),
    ).toBe(INVALID_CREDENTIALS_MESSAGE);
  });

  it("passes a denial's own reason through", () => {
    expect(
      loginFailure(
        new SessionError("account disabled", "permission_denied", 403),
      ),
    ).toBe("account disabled");
  });

  it("calls anything else sign-in unavailable, with why", () => {
    expect(
      loginFailure(
        new SessionError("the iam plane did not answer", "unavailable", 503),
      ),
    ).toBe(`${SIGN_IN_UNAVAILABLE_MESSAGE}: the iam plane did not answer`);
    expect(loginFailure(new TypeError("Failed to fetch"))).toBe(
      `${SIGN_IN_UNAVAILABLE_MESSAGE}: Failed to fetch`,
    );
  });
});
