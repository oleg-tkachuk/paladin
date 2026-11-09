import { describe, expect, it } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import { LIMES_CODE_KEY, PALADIN_ERROR_DOMAIN } from "@/hooks/errorContract";

import {
  budgetSaveFailure,
  LIMES_TENANT_BUDGET_BUSY,
} from "./budgetSaveFailure";

function failed(code: Code, limes?: string) {
  return new ConnectError(
    "x",
    code,
    undefined,
    limes === undefined
      ? []
      : [
          {
            desc: ErrorInfoSchema,
            value: create(ErrorInfoSchema, {
              domain: PALADIN_ERROR_DOMAIN,
              metadata: { [LIMES_CODE_KEY]: limes },
            }),
          },
        ],
  );
}

describe("budgetSaveFailure", () => {
  it("reads a budget the tenant's calls held as busy, to send again", () => {
    expect(
      budgetSaveFailure(failed(Code.Unavailable, LIMES_TENANT_BUDGET_BUSY)),
    ).toBe("busy");
  });

  it("reads the OCC guard as a conflict", () => {
    expect(budgetSaveFailure(failed(Code.Aborted, "conflict"))).toBe(
      "conflict",
    );
    expect(budgetSaveFailure(failed(Code.Aborted))).toBe("conflict");
  });

  it("leaves any other failure to the server's message", () => {
    expect(budgetSaveFailure(failed(Code.Unavailable))).toBe("other");
    expect(
      budgetSaveFailure(failed(Code.Unavailable, "store_unavailable")),
    ).toBe("other");
    expect(budgetSaveFailure(new Error("x"))).toBe("other");
  });
});
