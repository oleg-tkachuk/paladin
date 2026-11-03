/**
 * The event-ingest pipeline end to end on the compose stack: a storage event,
 * posted the way SeaweedFS posts one to the ingest webhook, promotes a PENDING
 * object and leaves the audit row the data-plane RPC would.
 *
 * The cluster's ingest listens on NATS and has no webhook to post to, so the
 * spec skips there. A delete event is not driven here: making bytes vanish from
 * the store needs a signed S3 delete this harness has no client for; it stays
 * with the unit and integration tests.
 */
import { createClient } from "@connectrpc/connect";

import { test, expect } from "./fixtures/resources";
import {
  INGEST_URL,
  adminTransport,
  objectState,
  seedAdminTenantID,
  seedPendingObject,
  seedPhysicalBucket,
  softDeleteObject,
} from "./fixtures/seed";
import { AuditLogService } from "@/gen/paladin/admin/v1/audit_service_pb";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";

// The route the webhook driver serves SeaweedFS notifications on, and the
// event type SeaweedFS names a new file with.
const SEAWEEDFS_ROUTE = "/webhook/seaweedfs";
const SEAWEEDFS_CREATE = "create";
// The filer directory the S3 gateway keeps its buckets under.
const SEAWEEDFS_BUCKETS_DIR = "/buckets";
// What an ingested transition is recorded as (internal/eventingest/audit.go).
const INGEST_UPLOAD_ACTION = "ingest.ObjectUploaded";
const INGEST_ACTOR = "system:event-ingest";
const NANOS_PER_MILLI = 1_000_000;
const AUDIT_PAGE_SIZE = 50;
const READY_TIMEOUT_MS = 30_000;
const PROMOTE_TIMEOUT_MS = 15_000;
const BODY = "ingested by a storage event\n";

test.describe("Event ingest", () => {
  test.skip(!INGEST_URL, "this stack's ingest has no webhook to post to");

  test("a storage event promotes a pending object and is audited", async ({
    request,
    makeCollection,
  }) => {
    await expect
      .poll(
        async () =>
          (
            await request.get(`${INGEST_URL}/readyz`).catch(() => null)
          )?.status(),
        { timeout: READY_TIMEOUT_MS },
      )
      .toBe(200);

    const tenantId = await seedAdminTenantID();
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });
    const obj = await seedPendingObject({
      tenantId,
      collection: collection.collection,
      body: BODY,
    });
    expect(await objectState(obj.name)).toBe(ObjectState.PENDING);

    // The shape SeaweedFS's filer posts: the full filer path, and the new
    // entry with the bytes' size, which must match the registration. A fresh
    // commit time keeps the event's dedup id its own.
    const posted = await request.post(`${INGEST_URL}${SEAWEEDFS_ROUTE}`, {
      data: {
        key: `${SEAWEEDFS_BUCKETS_DIR}/${bucket.bucketId}/${tenantId}/${obj.collection}/${obj.key}`,
        event_type: SEAWEEDFS_CREATE,
        message: {
          new_entry: {
            commit_ts_ns: Date.now() * NANOS_PER_MILLI,
            attributes: { file_size: Buffer.byteLength(BODY) },
          },
        },
      },
    });
    expect(posted.ok(), await posted.text()).toBe(true);

    await expect
      .poll(() => objectState(obj.name), { timeout: PROMOTE_TIMEOUT_MS })
      .toBe(ObjectState.AVAILABLE);

    const audit = createClient(AuditLogService, adminTransport());
    const { entries } = await audit.listAuditLog({
      filter: `action == "${INGEST_UPLOAD_ACTION}" && search.contains("${obj.key}")`,
      page: { pageSize: AUDIT_PAGE_SIZE },
    });
    expect(entries.length, "the promotion is in the audit log").toBe(1);
    expect(entries[0].actorSubject).toBe(INGEST_ACTOR);

    await softDeleteObject(obj.name);
  });
});
