import { describe, expect, it } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";

import { create } from "@bufbuild/protobuf";

import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import { ErrorReason } from "@/gen/paladin/common/v1/error_reason_pb";
import {
  PALADIN_ERROR_DOMAIN,
  errorMessage,
  errorReason,
  isAbortError,
} from "./errorContract";

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

/** A ConnectError carrying one ErrorInfo, as it arrives off the wire. */
function withInfo(domain: string, reason: string): ConnectError {
  const info = create(ErrorInfoSchema, { domain, reason });
  return new ConnectError("refused", Code.FailedPrecondition, undefined, [
    { desc: ErrorInfoSchema, value: info },
  ]);
}

describe("errorReason", () => {
  it("reads the server's reason", () => {
    expect(
      errorReason(
        withInfo(PALADIN_ERROR_DOMAIN, "ERROR_REASON_TENANT_ALREADY_DELETED"),
      ),
    ).toBe(ErrorReason.TENANT_ALREADY_DELETED);
    expect(
      errorReason(
        withInfo(PALADIN_ERROR_DOMAIN, "ERROR_REASON_PUBLIC_COLLECTION_RULE"),
      ),
    ).toBe(ErrorReason.PUBLIC_COLLECTION_RULE);
  });

  it("ignores another domain's reason", () => {
    expect(
      errorReason(
        withInfo("googleapis.com", "ERROR_REASON_TENANT_ALREADY_DELETED"),
      ),
    ).toBe(ErrorReason.UNSPECIFIED);
  });

  it("reads a reason newer than the console as none", () => {
    expect(
      errorReason(
        withInfo(PALADIN_ERROR_DOMAIN, "ERROR_REASON_FROM_THE_FUTURE"),
      ),
    ).toBe(ErrorReason.UNSPECIFIED);
  });

  it("has none for an error without details, or not a ConnectError", () => {
    expect(errorReason(new ConnectError("x", Code.Internal))).toBe(
      ErrorReason.UNSPECIFIED,
    );
    expect(errorReason(new Error("x"))).toBe(ErrorReason.UNSPECIFIED);
  });
});
