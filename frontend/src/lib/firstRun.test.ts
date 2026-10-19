import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { GetPlatformStatsResponseSchema } from "@/gen/paladin/admin/v1/system_service_pb";

import {
  BOOTSTRAP_TENANT_COUNT,
  firstRunPending,
  firstRunSteps,
} from "./firstRun";

const n = (v: number) => BigInt(v);

const stats = (o: {
  backends?: number;
  tenants?: number;
  buckets?: number;
  collections?: number;
  objects?: number;
  tokens?: number;
  rls?: boolean;
}) =>
  create(GetPlatformStatsResponseSchema, {
    backends: { total: n(o.backends ?? 0) },
    tenants: { total: n(o.tenants ?? BOOTSTRAP_TENANT_COUNT) },
    buckets: { total: n(o.buckets ?? 0) },
    collections: { total: n(o.collections ?? 0) },
    rls: {
      available: o.rls ?? true,
      objects: { totalCount: n(o.objects ?? 0) },
      apiTokens: { total: n(o.tokens ?? 0) },
    },
  });

const doneOf = (s: ReturnType<typeof stats>) =>
  Object.fromEntries(firstRunSteps(s).map((x) => [x.id, x.done]));

describe("firstRunSteps", () => {
  it("has nothing done on a fresh install, the platform tenant aside", () => {
    expect(doneOf(stats({}))).toEqual({
      backend: false,
      tenant: false,
      bucket: false,
      collection: false,
      object: false,
      token: false,
    });
  });

  it("counts a tenant past the bootstrap one", () => {
    expect(doneOf(stats({ tenants: BOOTSTRAP_TENANT_COUNT + 1 })).tenant).toBe(
      true,
    );
  });

  it("does not know the census steps while the worker is down", () => {
    const d = doneOf(stats({ rls: false, objects: 5, tokens: 5 }));
    expect(d.object).toBeUndefined();
    expect(d.token).toBeUndefined();
  });
});

describe("firstRunPending", () => {
  const all = {
    backends: 1,
    tenants: BOOTSTRAP_TENANT_COUNT + 1,
    buckets: 1,
    collections: 1,
    objects: 1,
    tokens: 1,
  };
  it("goes away once every step is done", () => {
    expect(firstRunPending(firstRunSteps(stats(all)))).toBe(false);
  });
  it("stays while one is not", () => {
    expect(firstRunPending(firstRunSteps(stats({ ...all, buckets: 0 })))).toBe(
      true,
    );
  });
  it("is not held up by steps it cannot know", () => {
    expect(firstRunPending(firstRunSteps(stats({ ...all, rls: false })))).toBe(
      false,
    );
  });
});
