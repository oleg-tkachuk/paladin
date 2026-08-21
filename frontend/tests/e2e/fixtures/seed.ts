/**
 * Per-test fixture seeders via direct Connect-RPC.
 *
 * Spec: spec.md FR-002 (independence), FR-003's setup
 * exception (RPC seeding is allowed; verification still goes
 * through the UI). Helpers cache the platform-admin access
 * token in module scope after the first IAM Login so we don't
 * pay the password-hash round-trip on every test.
 *
 * Why direct Connect-Web instead of through the BFF: this
 * code runs inside Playwright's Node worker, not inside the
 * browser. The BFF only exists as a Next.js route handler
 * reachable via the `ui` container. The test stack exposes
 * the API + admin planes directly on localhost (8085 / 8090),
 * so we use them.
 *
 * Audience exchange: AuthService.Login mints a token for the
 * `requestedAudience` field. For admin-plane work we request
 * `paladin-admin`; for iam-plane work `paladin-iam`. Token caching is
 * keyed by audience so a single seedTenant() call doesn't
 * burn two logins.
 */
import { createClient, ConnectError, Code } from "@connectrpc/connect";
// connect-NODE (not -web): this runs in Playwright's Node worker. The web
// transport's fetch path mis-handles a large (gzip-compressed) unary response
// in that worker — CreateTenant's multi-KB Cedar-policy body threw
// "Cannot read properties of undefined (reading 'length')" while the tiny
// Login response worked. The Node transport uses Node http + zlib directly.
import { createConnectTransport } from "@connectrpc/connect-node";

import { AuthService } from "@/gen/paladin/iam/v1/auth_service_pb";
import { ObjectService } from "@/gen/paladin/data/v1/object_service_pb";
import { UserService } from "@/gen/paladin/iam/v1/user_service_pb";
import { TenantService } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { BackendService } from "@/gen/paladin/admin/v1/backend_service_pb";
import { StorageKind } from "@/gen/paladin/admin/v1/types_pb";
import { BucketService } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { CollectionService } from "@/gen/paladin/admin/v1/collection_service_pb";
import {
  CapabilityService,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import { SEEDED_ADMIN } from "./credentials";
import { uniqueSlug, uniqueDisplayName } from "./unique";

// ─── plane base URLs (test stack) ──────────────────────────
const IAM_URL = process.env.PALADIN_E2E_IAM_URL ?? "http://localhost:8085";
const ADMIN_URL = process.env.PALADIN_E2E_ADMIN_URL ?? "http://localhost:8090";
const DATA_URL = process.env.PALADIN_E2E_DATA_URL ?? "http://localhost:8080";

// ─── token cache: audience → access JWT ────────────────────
// Lifetime: process-scoped. Playwright spawns one Node worker
// process per shard; tokens live as long as the worker does.
// AuthService.Login returns short-lived (≤ 15 min) tokens —
// the suite finishes in <3 min, so we never expire in
// practice. A future refresh-on-expiry path would call
// AuthService.ExchangeAudience instead of re-logging in.
const tokenCache = new Map<string, string>();

async function getAdminToken(): Promise<string> {
  const cached = tokenCache.get("paladin-admin");
  if (cached) return cached;
  const iam = createClient(
    AuthService,
    createConnectTransport({
      baseUrl: IAM_URL,
      httpVersion: "1.1",
      useBinaryFormat: false,
    }),
  );
  const res = await iam.login({
    subject: SEEDED_ADMIN.subject,
    password: SEEDED_ADMIN.password,
    requestedAudience: "paladin-admin",
  });
  const access = res.tokens?.accessToken;
  if (!access) {
    throw new Error(
      `seed.ts: Login(audience=paladin-admin) returned no access_token — ` +
        `check the bootstrap container provisioned ${SEEDED_ADMIN.subject}`,
    );
  }
  tokenCache.set("paladin-admin", access);
  return access;
}

// Shared admin transport: one createConnectTransport call per
// helper would force a new fetch pool on every seed call. We
// build it once and reuse across TenantService / BucketService.
async function getDataToken(): Promise<string> {
  const cached = tokenCache.get("paladin-data");
  if (cached) return cached;
  const client = createClient(
    AuthService,
    createConnectTransport({
      baseUrl: IAM_URL,
      httpVersion: "1.1",
      useBinaryFormat: false,
    }),
  );
  const res = await client.login({
    subject: SEEDED_ADMIN.subject,
    password: SEEDED_ADMIN.password,
    requestedAudience: "paladin-data",
  });
  const access = res.tokens?.accessToken;
  if (!access) {
    throw new Error(
      "seed.ts: Login(audience=paladin-data) returned no access_token",
    );
  }
  tokenCache.set("paladin-data", access);
  return access;
}

// dataTransport mirrors adminTransport for the data plane: same JSON codec,
// same idempotency-key injection, different audience.
function dataTransport() {
  return createConnectTransport({
    baseUrl: DATA_URL,
    httpVersion: "1.1",
    useBinaryFormat: false,
    interceptors: [
      (next) => async (req) => {
        req.header.set("Authorization", `Bearer ${await getDataToken()}`);
        if (!req.header.has("Idempotency-Key")) {
          req.header.set("Idempotency-Key", crypto.randomUUID());
        }
        return next(req);
      },
    ],
  });
}

function adminTransport() {
  return createConnectTransport({
    baseUrl: ADMIN_URL,
    httpVersion: "1.1",
    useBinaryFormat: false, // JSON codec — seed builds plain literals, not create()d messages
    interceptors: [
      (next) => async (req) => {
        const token = await getAdminToken();
        req.header.set("Authorization", `Bearer ${token}`);
        // Create* RPCs are gated by the idempotency middleware
        // (RequireOnCreate=true). The production transport
        // (src/lib/connect/transport.ts) auto-injects a UUID; the
        // seed transport must do the same or every Create* seed
        // fails with "missing Idempotency-Key header".
        if (!req.header.has("Idempotency-Key")) {
          req.header.set("Idempotency-Key", crypto.randomUUID());
        }
        return next(req);
      },
    ],
  });
}

function tenantAdminClient() {
  return createClient(TenantService, adminTransport());
}

function bucketAdminClient() {
  return createClient(BucketService, adminTransport());
}

function backendAdminClient() {
  return createClient(BackendService, adminTransport());
}

export interface SeededBackend {
  backendId: string;
  resourceVersion: string;
}

/**
 * Create a storage backend and immediately disable it (feature 002).
 * Used by the disabled-backend UI test to assert the "Disabled" badge,
 * non-selectable scope row, and enable toggle. The backend is registered
 * with provision skipped (no real S3 bucket needed) then flipped off via
 * SetBackendEnabled. UUID-suffixed id avoids collisions across tests.
 */
export async function seedDisabledBackend(opts?: {
  idPrefix?: string;
}): Promise<SeededBackend> {
  const client = backendAdminClient();
  const backendId = uniqueSlug(opts?.idPrefix ?? "disabled-be");
  const created = await client.createBackend({
    backendId,
    backend: {
      backendId,
      displayName: uniqueDisplayName("Disabled BE"),
      kind: StorageKind.S3_COMPATIBLE,
      endpoint: "http://garage.invalid:3900",
      region: "us-east-1",
      forcePathStyle: true,
      credentialsSecretRef: "e2e://disabled-backend-never-used",
    },
  });
  const disabled = await client.setBackendEnabled({
    name: `storageBackends/${backendId}`,
    enabled: false,
    resourceVersion: created.resourceVersion,
  });
  return { backendId, resourceVersion: disabled.resourceVersion };
}

function collectionAdminClient() {
  return createClient(CollectionService, adminTransport());
}

/**
 * Like adminTransport() but with an extra interceptor that
 * forces a CUSTOM Idempotency-Key on every outbound request.
 * Used by US4's double-submit test to fire two Issue calls
 * with the same key and observe the middleware reflective
 * replay collapse them.
 */
function adminTransportWithKey(key: string) {
  return createConnectTransport({
    baseUrl: ADMIN_URL,
    httpVersion: "1.1",
    useBinaryFormat: false, // JSON codec — seed builds plain literals, not create()d messages
    interceptors: [
      (next) => async (req) => {
        const token = await getAdminToken();
        req.header.set("Authorization", `Bearer ${token}`);
        // Override any auto-injected key with the test's.
        req.header.set("Idempotency-Key", key);
        return next(req);
      },
    ],
  });
}

function capabilityAdminClient(opts?: { idempotencyKey?: string }) {
  if (opts?.idempotencyKey) {
    return createClient(
      CapabilityService,
      adminTransportWithKey(opts.idempotencyKey),
    );
  }
  return createClient(CapabilityService, adminTransport());
}

// ─── seeded entity shapes ──────────────────────────────────
export interface SeededTenant {
  tenantId: string; // UUID
  slug: string;
  displayName: string;
  resourceVersion: bigint;
}

/**
 * Create a tenant via TenantService.CreateTenant. Slug +
 * display name are UUID-suffixed by default to satisfy the
 * UNIQUE constraints from migration 033. Pass overrides only
 * when a specific shape matters (e.g. US2 wants the prefixes
 * "acme" and "globex" so the topbar selector test has
 * predictable labels).
 *
 * The platform-admin Bearer is fetched on first call and
 * cached for the lifetime of the worker (see tokenCache).
 */
export async function seedTenant(opts?: {
  slugPrefix?: string;
  displayNamePrefix?: string;
}): Promise<SeededTenant> {
  const slug = uniqueSlug(opts?.slugPrefix ?? "acme");
  const displayName = uniqueDisplayName(opts?.displayNamePrefix ?? "Acme E2E");
  const client = tenantAdminClient();
  const res = await client.createTenant({
    tenantId: "", // server-generated UUIDv7
    tenant: {
      tenantId: "",
      name: "",
      slug,
      displayName,
      inheritedCedarPolicy: "",
      labels: {},
      resourceVersion: "",
    },
    defaultBucket: "",
  });
  const created = res;
  if (!created.tenantId || !created.slug) {
    throw new Error(
      `seedTenant: server returned an empty tenant payload — got ${JSON.stringify(created)}`,
    );
  }
  return {
    tenantId: created.tenantId,
    slug: created.slug,
    displayName: created.displayName,
    resourceVersion: BigInt(created.resourceVersion || "0"),
  };
}

// ─── bucket seeding ────────────────────────────────────────
//
// Originally planned for US3 (tasks.md T024) but pulled
// forward into Phase 2 because US2's repurposed scenarios
// (backend/bucket scope switching) need at least one bucket
// to exist for the picker to render selectable items.

export interface SeededBucket {
  /** Backend ID this bucket lives in (e.g. "primary"). */
  backendId: string;
  /** Physical bucket name. UUID-suffixed via uniqueSlug. */
  bucketId: string;
  /** Human-facing display name. UUID-suffixed. */
  displayName: string;
}

/**
 * Create a bucket via BucketService.CreateBucket under the
 * given backend. Defaults to the chart-default `primary`
 * backend the bootstrap container provisions.
 *
 * `provisionOnBackend` defaults to false: the metadata tests —
 * scope picker, bucket lists, collection binding — assert rows,
 * not S3 I/O, and skipping provision shaves ~1s per call.
 *
 * Pass `provision: true` when the test will actually move bytes.
 * A presigned PUT against an unprovisioned bucket fails with
 * NoSuchBucket, which reads as a broken URL rather than a bucket
 * that was never created.
 */
export async function seedBucket(opts?: {
  backendId?: string;
  bucketIdPrefix?: string;
  displayNamePrefix?: string;
  provision?: boolean;
}): Promise<SeededBucket> {
  const backendId = opts?.backendId ?? "primary";
  const bucketId = uniqueSlug(opts?.bucketIdPrefix ?? "e2e-bucket");
  const displayName = uniqueDisplayName(
    opts?.displayNamePrefix ?? "E2E Bucket",
  );
  const client = bucketAdminClient();
  await client.createBucket({
    parent: `storageBackends/${backendId}`,
    bucketId,
    bucket: {
      backendId,
      bucketId,
      displayName,
      region: "",
      labels: {},
      cedarPolicy: "",
    },
    provisionOnBackend: opts?.provision ?? false,
  });
  return { backendId, bucketId, displayName };
}

/**
 * The bucket that physically exists in the test stack's MinIO, registered in
 * Paladin so objects can actually be written to it.
 *
 * seedBucket({provision: true}) is not an option here: provisioning is
 * asynchronous and completed by the worker plane, which this stack leaves
 * out on purpose (see docker-compose.test.yaml). The bucket the minio-setup
 * container creates is the one real place bytes can land.
 *
 * Idempotent — every object test shares it.
 */
export async function seedPhysicalBucket(): Promise<SeededBucket> {
  const backendId = "primary";
  const bucketId = process.env.PALADIN_E2E_S3_BUCKET ?? "paladin-e2e";
  try {
    await bucketAdminClient().createBucket({
      parent: `storageBackends/${backendId}`,
      bucketId,
      bucket: { backendId, bucketId, displayName: "E2E physical bucket" },
      provisionOnBackend: false,
    });
  } catch (e) {
    // Already registered by an earlier test in this run. The conflict
    // surfaces as FailedPrecondition rather than AlreadyExists — the admin
    // plane reports it as a state conflict — so both are tolerated.
    const conflict =
      e instanceof ConnectError &&
      (e.code === Code.AlreadyExists || e.code === Code.FailedPrecondition);
    if (!conflict) throw e;
  }
  return { backendId, bucketId, displayName: "E2E physical bucket" };
}

// ─── collection seeding ────────────────────────────────────

export interface SeededCollection {
  tenantId: string;
  /** Bucket resource name: `storageBackends/{backend}/buckets/{name}`. */
  bucket: string;
  /** Collection identifier — `e2e/<8-hex>`. */
  collection: string;
  displayName: string;
}

/**
 * Create a Collection under the given (tenant, bucket) via
 * CollectionService.CreateCollection. The key path is
 * `e2e/<8-hex>` so concurrent tests don't collide on the
 * (tenant_id, object_key) UNIQUE constraint.
 */
export async function seedCollection(opts: {
  tenantId: string;
  bucket: SeededBucket;
  collectionPrefix?: string;
}): Promise<SeededCollection> {
  const collection = `e2e/${uniqueSlug(opts.collectionPrefix ?? "key").replace(/^[^-]+-/, "")}`;
  const displayName = uniqueDisplayName("E2E Key");
  const bucketResourceName = `storageBackends/${opts.bucket.backendId}/buckets/${opts.bucket.bucketId}`;
  const client = collectionAdminClient();
  await client.createCollection({
    parent: `tenants/${opts.tenantId}`,
    collection,
    collectionResource: {
      name: "",
      tenantId: opts.tenantId,
      collection,
      displayName,
      bucket: bucketResourceName,
      cedarPolicy: "",
    },
  });
  return {
    tenantId: opts.tenantId,
    bucket: bucketResourceName,
    collection,
    displayName,
  };
}

// ─── capability seeding ────────────────────────────────────

export interface SeededCapability {
  /** Server-minted capability ID (UUID). */
  id: string;
  tenantId: string;
  /** Subject the capability was issued to (e.g. `e2e-agent-<hex>`). */
  subject: string;
}

/**
 * Issue a capability token via CapabilityService.Issue. Minimal
 * caveats — single op `get`, empty prefixes, finite small budget.
 * The actual caveat shape doesn't matter for US4's tests; we
 * only care that the row materialises in `/capabilities`.
 *
 * `opts.idempotencyKey` is the FR-008 hook: when set, the Issue
 * RPC is fired with that exact header value, exercising the
 * middleware's reflective replay path. Two calls with the same
 * key MUST return the same capability ID (the second call hits
 * the cached response).
 */
export async function seedCapability(opts: {
  tenantId: string;
  subjectPrefix?: string;
  idempotencyKey?: string;
}): Promise<SeededCapability> {
  const subject = uniqueSlug(opts.subjectPrefix ?? "e2e-agent");
  const client = capabilityAdminClient({
    idempotencyKey: opts.idempotencyKey,
  });
  const res = await client.issue({
    subject: {
      kind: PrincipalKind.AGENT,
      tenantId: opts.tenantId,
      subject,
    },
    audience: ["paladin-data"],
    caveats: {
      ops: ["get"],
      resourcePrefixes: [],
      resourceUris: [],
      maxRequests: 100,
      maxBudgetAmount: 0,
      unitCode: "",
      allowTaintedRead: false,
      idempotencyKeyRequired: false,
      sourceIpCidr: [],
    },
    ttlSeconds: BigInt(900),
  });
  const id = (res as { capability?: { id?: string } }).capability?.id;
  if (!id) {
    throw new Error(
      `seedCapability: server returned no capability id — got ${JSON.stringify(res)}`,
    );
  }
  return { id, tenantId: opts.tenantId, subject };
}

// ─── cross-tenant membership seeding ───────────────────────
//
// SwitchTenant (US2 tenant switching) needs the signed-in subject to hold a
// users row in a SECOND tenant — a "membership" under the 1:1-per-tenant
// user model. platform.admin may create users in foreign tenants, so we
// seed: fresh tenant → CreateUser(same subject) in it. The membership's
// password is irrelevant to switching (SwitchTenant re-mints off the
// caller's existing identity, no password re-check), but CreateUser
// requires one ≥12 chars.

async function getIamToken(): Promise<string> {
  const cached = tokenCache.get("paladin-iam");
  if (cached) return cached;
  const iam = createClient(
    AuthService,
    createConnectTransport({
      baseUrl: IAM_URL,
      httpVersion: "1.1",
      useBinaryFormat: false,
    }),
  );
  const res = await iam.login({
    subject: SEEDED_ADMIN.subject,
    password: SEEDED_ADMIN.password,
    requestedAudience: "paladin-iam",
  });
  const access = res.tokens?.accessToken;
  if (!access) {
    throw new Error(
      `seed.ts: Login(audience=paladin-iam) returned no access_token — ` +
        `check the bootstrap container provisioned ${SEEDED_ADMIN.subject}`,
    );
  }
  tokenCache.set("paladin-iam", access);
  return access;
}

function iamAdminTransport() {
  return createConnectTransport({
    baseUrl: IAM_URL,
    httpVersion: "1.1",
    useBinaryFormat: false,
    interceptors: [
      (next) => async (req) => {
        const token = await getIamToken();
        req.header.set("Authorization", `Bearer ${token}`);
        if (!req.header.has("Idempotency-Key")) {
          req.header.set("Idempotency-Key", crypto.randomUUID());
        }
        return next(req);
      },
    ],
  });
}

export interface SeededMembership {
  /** The second tenant the seeded admin subject is now a member of. */
  tenantId: string;
  slug: string;
  displayName: string;
}

/**
 * Seed a second tenant + a users row for SEEDED_ADMIN's subject inside it,
 * so the ScopePicker's tenant switcher has a real target. Roles:
 * tenant.admin — deliberately NOT platform.admin, proving roles are
 * per-membership.
 */
export async function seedTenantMembership(): Promise<SeededMembership> {
  const tenant = await seedTenant({
    slugPrefix: "switch",
    displayNamePrefix: "Switch Target",
  });
  const users = createClient(UserService, iamAdminTransport());
  await users.createUser({
    parent: `tenants/${tenant.tenantId}`,
    subject: SEEDED_ADMIN.subject,
    displayName: "e2e-admin (switch membership)",
    initialPassword: "e2e-switch-not-a-secret-2026",
    roles: ["tenant.admin"],
    scopes: [],
  });
  return {
    tenantId: tenant.tenantId,
    slug: tenant.slug,
    displayName: tenant.displayName,
  };
}

// ─── objects (data plane) ──────────────────────────────────

/**
 * The tenant the seeded admin belongs to. Objects must be seeded into a
 * collection this tenant owns: the data plane scopes every request to the
 * caller's tenant, so a platform token cannot upload into someone else's
 * namespace — which is the isolation working, not a limitation to route
 * around.
 */
export async function seedAdminTenantID(): Promise<string> {
  const client = createClient(TenantService, adminTransport());
  const res = await client.getTenant({ name: "tenants/platform" });
  return res.tenantId;
}

export interface SeededObject {
  objectId: string;
  key: string;
  collection: string;
  tenantId: string;
  /** Canonical resource name as the server minted it — the only form the
   *  data-plane RPCs accept. Object names address the object_id, not the
   *  key, which may itself contain slashes. */
  name: string;
}

/**
 * Create an object through the real two-step upload: UploadObject reserves
 * the row and hands back a presigned PUT, CompleteObject promotes it to
 * AVAILABLE. The bytes go straight to the backend, which is the whole point
 * of the design — Paladin never sees them.
 */
export async function seedObject(opts: {
  tenantId: string;
  collection: string;
  key?: string;
  body?: string;
}): Promise<SeededObject> {
  const key = opts.key ?? `e2e/${uniqueSlug("obj")}.txt`;
  const body = opts.body ?? "paladin e2e payload\n";
  const client = createClient(ObjectService, dataTransport());

  const up = await client.uploadObject({
    parent: `tenants/${opts.tenantId}/collections/${opts.collection}`,
    key,
    contentType: "text/plain",
    sizeHintBytes: BigInt(Buffer.byteLength(body)),
  });
  const presigned = up.uploadUrl;
  if (!presigned?.url) {
    throw new Error(`seed.ts: UploadObject returned no upload_url for ${key}`);
  }
  // required_headers are part of the signature — dropping one makes the
  // backend reject the PUT with a signature mismatch, which reads as a
  // credentials problem rather than a missing header.
  const put = await fetch(presigned.url, {
    method: presigned.method || "PUT",
    headers: { "Content-Type": "text/plain", ...presigned.requiredHeaders },
    body,
  });
  if (!put.ok) {
    throw new Error(
      `seed.ts: presigned PUT failed: ${put.status} ${await put.text()}`,
    );
  }
  const name = up.object?.name ?? "";
  const done = await client.completeObject({ name });
  return {
    objectId: done.objectId,
    key,
    collection: opts.collection,
    tenantId: opts.tenantId,
    name: done.name || name,
  };
}

/** Soft-delete an object through the data plane, by its canonical name. */
export async function softDeleteObject(name: string): Promise<void> {
  await createClient(ObjectService, dataTransport()).deleteObject({ name });
}
