import { describe, expect, it } from "vitest";

import {
  HEALTH_TOKEN_ENV,
  HEALTH_TOKEN_FILE_ENV,
  resolveHealthToken,
} from "./token";

const MOUNTED = "/etc/paladin-health-token/token";

describe("resolveHealthToken", () => {
  it("reads the mounted file, without its trailing newline", () => {
    const read = (p: string) => {
      expect(p).toBe(MOUNTED);
      return "from-file\n";
    };
    expect(resolveHealthToken({ [HEALTH_TOKEN_FILE_ENV]: MOUNTED }, read)).toBe(
      "from-file",
    );
  });

  it("prefers the file over the plain variable", () => {
    const env = {
      [HEALTH_TOKEN_FILE_ENV]: MOUNTED,
      [HEALTH_TOKEN_ENV]: "from-env",
    };
    expect(resolveHealthToken(env, () => "from-file")).toBe("from-file");
  });

  it("falls back to the plain variable in dev", () => {
    expect(resolveHealthToken({ [HEALTH_TOKEN_ENV]: "from-env" })).toBe(
      "from-env",
    );
  });

  it("is empty when neither is set", () => {
    expect(resolveHealthToken({})).toBe("");
  });

  it("refuses a named file it cannot read", () => {
    const read = () => {
      throw new Error("ENOENT");
    };
    expect(() =>
      resolveHealthToken({ [HEALTH_TOKEN_FILE_ENV]: MOUNTED }, read),
    ).toThrow(/cannot be read: ENOENT/);
  });
});
