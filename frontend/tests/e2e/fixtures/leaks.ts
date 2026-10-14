/**
 * What a run leaves behind, measured rather than assumed.
 *
 * Every fixture removes what it made, and every removal reports its own
 * failure — but a report is a line in a log nobody reads when the run is
 * green. The leftovers it described piled up anyway: buckets whose SeaweedFS
 * collections each held volume slots until the store refused new buckets,
 * backends no list ever cleared, tenants in a trash that only grew.
 *
 * So the run takes an inventory of fixture-shaped resources before it starts
 * and again after its last test, and the leaks project fails on anything the
 * second has that the first did not. Comparing against the run's own start
 * keeps the check fair on a shared stack: rows someone else left are not this
 * run's to answer for.
 */
import { createClient } from "@connectrpc/connect";

import { BackendService } from "@/gen/paladin/admin/v1/backend_service_pb";
import { BucketService } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { TenantService } from "@/gen/paladin/admin/v1/tenant_service_pb";

import { adminTransport } from "./seed";
import { FIXTURE_SLUG_RE } from "./unique";

/** The inventory the environment project writes into its output directory. */
export const LEAK_BASELINE_FILE = "fixture-inventory.json";

/** One page of each listing; the stacks this runs on hold far fewer. */
const LIST_PAGE_SIZE = 200;

/** Fixture-shaped resources, by the identifier their fixture minted. */
export interface FixtureInventory {
  /** Tenants that take changes. */
  activeTenants: string[];
  /**
   * Tenants in the trash. A fixture's teardown purges what it trashes, and
   * nothing a fixture tenant holds can refuse that purge, so one left here
   * is a leak like any other.
   */
  trashedTenants: string[];
  /** Buckets, as storageBackends/{backend}/buckets/{bucket}. */
  buckets: string[];
  /** Storage backends, by id. */
  backends: string[];
}

/** Lists every fixture-shaped tenant, bucket and backend the stack holds. */
export async function fixtureInventory(): Promise<FixtureInventory> {
  const inventory: FixtureInventory = {
    activeTenants: [],
    trashedTenants: [],
    buckets: [],
    backends: [],
  };

  const tenants = createClient(TenantService, adminTransport());
  let pageToken = "";
  do {
    const res = await tenants.listTenants({
      page: { pageSize: LIST_PAGE_SIZE, pageToken },
      includeTrashed: true,
    });
    for (const t of res.tenants) {
      if (!FIXTURE_SLUG_RE.test(t.slug)) continue;
      (t.deletedAt ? inventory.trashedTenants : inventory.activeTenants).push(
        t.slug,
      );
    }
    pageToken = res.page?.nextPageToken ?? "";
  } while (pageToken);

  const buckets = createClient(BucketService, adminTransport());
  pageToken = "";
  do {
    // An empty parent lists across every backend.
    const res = await buckets.listBuckets({
      parent: "",
      page: { pageSize: LIST_PAGE_SIZE, pageToken },
    });
    for (const b of res.buckets) {
      if (FIXTURE_SLUG_RE.test(b.bucketId)) inventory.buckets.push(b.name);
    }
    pageToken = res.page?.nextPageToken ?? "";
  } while (pageToken);

  const backends = createClient(BackendService, adminTransport());
  pageToken = "";
  do {
    const res = await backends.listBackends({
      page: { pageSize: LIST_PAGE_SIZE, pageToken },
    });
    for (const b of res.backends) {
      if (FIXTURE_SLUG_RE.test(b.backendId))
        inventory.backends.push(b.backendId);
    }
    pageToken = res.page?.nextPageToken ?? "";
  } while (pageToken);

  return inventory;
}

/** What `after` holds that `before` did not, field by field. */
export function addedSince(
  before: FixtureInventory,
  after: FixtureInventory,
): FixtureInventory {
  const added = (was: string[], is: string[]) => {
    const seen = new Set(was);
    return is.filter((id) => !seen.has(id));
  };
  return {
    activeTenants: added(before.activeTenants, after.activeTenants),
    // A tenant that was there at the start, active or trashed, is not this
    // run's even if the run is what trashed it.
    trashedTenants: added(
      [...before.activeTenants, ...before.trashedTenants],
      after.trashedTenants,
    ),
    buckets: added(before.buckets, after.buckets),
    backends: added(before.backends, after.backends),
  };
}
