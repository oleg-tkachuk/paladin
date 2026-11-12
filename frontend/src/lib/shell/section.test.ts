import { ConnectError, Code } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";

import { settledSection } from "./section";

describe("settledSection", () => {
  it("shows what an answer carries", () => {
    expect(
      settledSection(
        { status: "fulfilled", value: { operations: [1, 2] } },
        (r) => r.operations,
      ),
    ).toEqual({
      status: "ok",
      data: [1, 2],
    });
  });

  it("says why a call failed, with the Connect code", () => {
    const refused = new ConnectError("no", Code.PermissionDenied);
    expect(settledSection({ status: "rejected", reason: refused })).toEqual({
      status: "unavailable",
      reason: "PermissionDenied: no",
      data: null,
    });
    expect(
      settledSection({ status: "rejected", reason: new Error("down") }).reason,
    ).toBe("down");
  });
});
