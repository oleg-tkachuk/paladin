import { describe, expect, it } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";

import { errorMessage, isAbortError } from "./errorContract";

// Pins the caller-facing half of the data-hook error contract (see
// README.md): errorMessage is what every mutation caller uses to turn a
// thrown value into a toast string. The hook-behavior half (a query sets
// `error` and resolves; a mutation rejects and leaves `error` untouched) is
// a DOM/renderHook test and rides the RTL wave the vitest.config.ts comment
// defers — this suite stays framework-free by design.
describe("errorMessage", () => {
  it("unwraps a ConnectError to its rawMessage", () => {
    const err = new ConnectError("bucket already exists", Code.AlreadyExists);
    // ConnectError prefixes the code onto .message; rawMessage is clean.
    expect(errorMessage(err)).toBe("bucket already exists");
  });

  it("uses a plain Error's message", () => {
    expect(errorMessage(new Error("boom"))).toBe("boom");
  });

  it("falls back when the Error has no message", () => {
    expect(errorMessage(new Error(""), "fallback")).toBe("fallback");
  });

  it("falls back for non-error throwables (string, null, undefined)", () => {
    expect(errorMessage("nope", "fb")).toBe("fb");
    expect(errorMessage(null, "fb")).toBe("fb");
    expect(errorMessage(undefined, "fb")).toBe("fb");
    expect(errorMessage({ weird: true }, "fb")).toBe("fb");
  });

  it("uses the default fallback when none is supplied", () => {
    expect(errorMessage(42)).toBe("Something went wrong");
  });

  it("prefers a ConnectError unwrap over the fallback", () => {
    const err = new ConnectError("denied", Code.PermissionDenied);
    expect(errorMessage(err, "fb")).toBe("denied");
  });
});

describe("isAbortError", () => {
  it("recognises a cancelled Connect request", () => {
    expect(isAbortError(new ConnectError("cancelled", Code.Canceled))).toBe(
      true,
    );
  });

  it("recognises a DOM AbortError", () => {
    expect(
      isAbortError(new DOMException("signal is aborted", "AbortError")),
    ).toBe(true);
  });

  it("does not swallow a real failure", () => {
    expect(isAbortError(new ConnectError("boom", Code.Internal))).toBe(false);
    expect(isAbortError(new Error("boom"))).toBe(false);
  });
});
