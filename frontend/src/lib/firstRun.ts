// The first things an empty installation needs, in order, read off the
// platform's own counts (GetPlatformStats). Pure, so the dashboard and its
// tests agree on when a step is done and when the checklist goes away.
import type { GetPlatformStatsResponse } from "@/gen/paladin/admin/v1/system_service_pb";
import { SECTION } from "@/lib/navigation/sections";

/**
 * Tenants a fresh install already has: the platform tenant the bootstrap
 * creates. A first tenant of one's own is one more than this.
 */
export const BOOTSTRAP_TENANT_COUNT = 1;

export interface FirstRunStep {
  id: "backend" | "tenant" | "bucket" | "collection" | "object" | "token";
  label: string;
  /** Done, not done, or unknown: the census behind it did not answer. */
  done: boolean | undefined;
  href: string;
}

export function firstRunSteps(stats: GetPlatformStatsResponse): FirstRunStep[] {
  const rls = stats.rls?.available ? stats.rls : undefined;
  // More than `floor` of them, or unknown without a count.
  const big = (n: bigint | undefined, floor = BigInt(0)) =>
    n === undefined ? undefined : n > floor;
  return [
    {
      id: "backend",
      label: "Register a storage backend",
      done: big(stats.backends?.total),
      href: SECTION.storageBackends,
    },
    {
      id: "tenant",
      label: "Create a tenant",
      done: big(stats.tenants?.total, BigInt(BOOTSTRAP_TENANT_COUNT)),
      href: SECTION.tenants,
    },
    {
      id: "bucket",
      label: "Create a bucket",
      done: big(stats.buckets?.total),
      href: SECTION.buckets,
    },
    {
      id: "collection",
      label: "Create a collection",
      done: big(stats.collections?.total),
      href: SECTION.collections,
    },
    {
      id: "object",
      label: "Upload a first object",
      done: rls ? big(rls.objects?.totalCount) : undefined,
      href: SECTION.upload,
    },
    {
      id: "token",
      label: "Issue a token for a client",
      done: rls ? big(rls.apiTokens?.total) : undefined,
      href: SECTION.tenants,
    },
  ];
}

/**
 * The checklist shows while a step is known not to be done. An unknown one
 * does not hold it up: with the worker down, a long-running install would
 * otherwise wear a first-run checklist for as long as the outage lasts.
 */
export function firstRunPending(steps: readonly FirstRunStep[]): boolean {
  return steps.some((s) => s.done === false);
}
