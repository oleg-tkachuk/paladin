import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { WhoAmIResponseSchema } from "@/gen/paladin/iam/v1/auth_service_pb";
import { UserSchema } from "@/gen/paladin/iam/v1/types_pb";

import { userFromWhoAmI } from "./user";

describe("userFromWhoAmI", () => {
  it("keeps the user and the tenant's slug, each in its own field", () => {
    const res = create(WhoAmIResponseSchema, {
      tenantSlug: "acme",
      user: create(UserSchema, {
        userId: "u-1",
        tenantId: "t-1",
        subject: "ada",
        displayName: "Ada",
        roles: ["tenant.admin"],
        resourceVersion: "7",
      }),
    });
    expect(userFromWhoAmI(res)).toEqual({
      userId: "u-1",
      tenantId: "t-1",
      tenantSlug: "acme",
      subject: "ada",
      displayName: "Ada",
      roles: ["tenant.admin"],
      resourceVersion: "7",
    });
  });

  it("refuses an answer without a user", () => {
    expect(() => userFromWhoAmI(create(WhoAmIResponseSchema, {}))).toThrow(
      /without a user/,
    );
  });
});
