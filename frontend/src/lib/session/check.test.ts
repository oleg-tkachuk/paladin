import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";

import {
  RETRY_AFTER_HEADER,
  SESSION_RETRY_BASE_MS,
  SESSION_RETRY_MAX_MS,
  classifySessionAnswer,
  classifySessionError,
  parseRetryAfter,
  sessionRetryDelayMs,
} from "./check";

const NOW = Date.parse("2026-11-24T12:00:00Z");

describe("classifySessionAnswer", () => {
  it.each([
    [
      "204, the session kept",
      { status: 204, retryAfter: null },
      { kind: "signed-in" },
    ],
    [
      "401, no session",
      { status: 401, retryAfter: null },
      { kind: "signed-out" },
    ],
    [
      "403, a refused origin",
      { status: 403, retryAfter: null },
      { kind: "signed-out" },
    ],
    [
      "503 without Retry-After",
      { status: 503, retryAfter: null },
      { kind: "unavailable", retryAfterMs: null },
    ],
    [
      "503 with Retry-After in seconds",
      { status: 503, retryAfter: "7" },
      { kind: "unavailable", retryAfterMs: 7_000 },
    ],
    [
      "503 with Retry-After as a date",
      { status: 503, retryAfter: "Tue, 24 Nov 2026 12:00:05 GMT" },
      { kind: "unavailable", retryAfterMs: 5_000 },
    ],
    [
      "429, rate limited",
      { status: 429, retryAfter: "2" },
      { kind: "unavailable", retryAfterMs: 2_000 },
    ],
    [
      "500 from the role",
      { status: 500, retryAfter: null },
      { kind: "unavailable", retryAfterMs: null },
    ],
    [
      "502 from the ingress",
      { status: 502, retryAfter: null },
      { kind: "unavailable", retryAfterMs: null },
    ],
    [
      "504 from the ingress",
      { status: 504, retryAfter: null },
      { kind: "unavailable", retryAfterMs: null },
    ],
    [
      "no answer at all",
      { networkError: true as const },
      { kind: "unavailable", retryAfterMs: null },
    ],
  ])("%s", (_, answer, want) => {
    expect(classifySessionAnswer(answer, NOW)).toEqual(want);
  });
});

describe("classifySessionError", () => {
  it("reads Unauthenticated as signed out", () => {
    expect(
      classifySessionError(
        new ConnectError("no session", Code.Unauthenticated),
        NOW,
      ),
    ).toEqual({ kind: "signed-out" });
  });

  it.each([
    Code.Unavailable,
    Code.DeadlineExceeded,
    Code.ResourceExhausted,
    Code.Internal,
    Code.Unknown,
  ])("reads code %s as unavailable", (code) => {
    expect(classifySessionError(new ConnectError("down", code), NOW)).toEqual({
      kind: "unavailable",
      retryAfterMs: null,
    });
  });

  it("takes the wait from the error's Retry-After", () => {
    const err = new ConnectError(
      "down",
      Code.Unavailable,
      new Headers({ [RETRY_AFTER_HEADER]: "3" }),
    );
    expect(classifySessionError(err, NOW)).toEqual({
      kind: "unavailable",
      retryAfterMs: 3_000,
    });
  });

  it("reads a fetch that never got an answer as unavailable", () => {
    expect(classifySessionError(new TypeError("Failed to fetch"), NOW)).toEqual(
      {
        kind: "unavailable",
        retryAfterMs: null,
      },
    );
  });

  it("reads any other refusal or failure as signed out", () => {
    expect(
      classifySessionError(new ConnectError("no", Code.PermissionDenied), NOW),
    ).toEqual({ kind: "signed-out" });
    expect(classifySessionError(new Error("bad user"), NOW)).toEqual({
      kind: "signed-out",
    });
  });
});

describe("parseRetryAfter", () => {
  it.each([
    [null, null],
    ["", null],
    ["soon", null],
    ["-1", null],
    ["0", 0],
    ["10", 10_000],
    ["Tue, 24 Nov 2026 11:59:00 GMT", 0],
    ["3600", SESSION_RETRY_MAX_MS],
  ])("reads %j as %j", (value, want) => {
    expect(parseRetryAfter(value, NOW)).toBe(want);
  });
});

describe("sessionRetryDelayMs", () => {
  it("doubles from the base up to the cap", () => {
    expect(sessionRetryDelayMs(0, null)).toBe(SESSION_RETRY_BASE_MS);
    expect(sessionRetryDelayMs(1, null)).toBe(2 * SESSION_RETRY_BASE_MS);
    expect(sessionRetryDelayMs(2, null)).toBe(4 * SESSION_RETRY_BASE_MS);
    expect(sessionRetryDelayMs(20, null)).toBe(SESSION_RETRY_MAX_MS);
  });

  it("waits at least as long as Retry-After asked", () => {
    expect(sessionRetryDelayMs(0, 9_000)).toBe(9_000);
    expect(sessionRetryDelayMs(5, 1_000)).toBe(SESSION_RETRY_MAX_MS);
  });
});
