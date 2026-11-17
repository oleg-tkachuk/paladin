import { describe, expect, it } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import { PALADIN_ERROR_DOMAIN } from "@/hooks/errorContract";
import { SECTION } from "@/lib/navigation/sections";
import {
  RETIRED_SLUGS_HREF,
  SLUG_HOLDER_KEY,
  SlugHolder,
  slugTakenBy,
  slugTakenHint,
} from "./slugHolder";

const SLUG_TAKEN = "ERROR_REASON_SLUG_TAKEN";

function refused(reason: string, metadata: Record<string, string> = {}) {
  const info = create(ErrorInfoSchema, {
    domain: PALADIN_ERROR_DOMAIN,
    reason,
    metadata,
  });
  return new ConnectError("taken", Code.AlreadyExists, undefined, [
    { desc: ErrorInfoSchema, value: info },
  ]);
}

describe("slugTakenBy", () => {
  it.each(Object.values(SlugHolder))("reads holder %s", (holder) => {
    expect(
      slugTakenBy(refused(SLUG_TAKEN, { [SLUG_HOLDER_KEY]: holder })),
    ).toBe(holder);
  });

  it("is null for a holder this console does not know", () => {
    expect(
      slugTakenBy(refused(SLUG_TAKEN, { [SLUG_HOLDER_KEY]: "elsewhere" })),
    ).toBeNull();
  });

  it("is null for a SLUG_TAKEN naming no holder", () => {
    expect(slugTakenBy(refused(SLUG_TAKEN))).toBeNull();
  });

  it("is null for another reason, even with a holder", () => {
    expect(
      slugTakenBy(
        refused("ERROR_REASON_DISPLAY_NAME_TAKEN", {
          [SLUG_HOLDER_KEY]: SlugHolder.Retired,
        }),
      ),
    ).toBeNull();
  });

  it("is null for an error off the wire", () => {
    expect(slugTakenBy(new Error("boom"))).toBeNull();
  });
});

describe("slugTakenHint", () => {
  it("sends a retired slug to the retired list", () => {
    const hint = slugTakenHint(SlugHolder.Retired);
    expect(hint.href).toBe(RETIRED_SLUGS_HREF);
    expect(RETIRED_SLUGS_HREF.startsWith(SECTION.trash + "#")).toBe(true);
  });

  it("sends a trashed holder to the trash", () => {
    expect(slugTakenHint(SlugHolder.Trashed).href).toBe(SECTION.trash);
  });

  it("offers nowhere to go for a live holder", () => {
    const hint = slugTakenHint(SlugHolder.Live);
    expect(hint.href).toBeUndefined();
    expect(hint.text).not.toBe("");
  });
});
