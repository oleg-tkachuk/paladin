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
import { BatchService } from "@/gen/paladin/data/v1/batch_service_pb";
import { MultipartUploadService } from "@/gen/paladin/data/v1/multipart_service_pb";
import { ChecksumAlgorithm } from "@/gen/paladin/common/v1/resource_pb";
import { UserService } from "@/gen/paladin/iam/v1/user_service_pb";
import { TenantService } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { QuotaService } from "@/gen/paladin/admin/v1/quota_service_pb";
import { EventSubscriptionService } from "@/gen/paladin/admin/v1/event_subscription_service_pb";
import { TenantBudgetService } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { CollectionService } from "@/gen/paladin/admin/v1/collection_service_pb";
import { BackendService } from "@/gen/paladin/admin/v1/backend_service_pb";
import { StorageKind } from "@/gen/paladin/admin/v1/types_pb";
import { BucketService } from "@/gen/paladin/admin/v1/bucket_service_pb";
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

// ─── token cache: audience → access JWT + its expiry ───────
//
// Lifetime: process-scoped. Playwright spawns one Node worker process per
// shard; tokens live as long as the worker does.
//
// This used to cache the token alone, on the reasoning that Login returns
// ≤15-minute tokens and "the suite finishes in <3 min, so we never expire in
// practice". That was true of 27 tests against the compose stack. Against a
// cluster the suite runs 28 minutes, and the token expired mid-run — every
// time, in whichever test happened to be running at the 15-minute mark. It
// looked like a different flaky test on every run, and I chased it through
// four wrong explanations before reading the error: "jwt: token expired".
//
// So: keep the expiry, and re-login before it lapses.
interface CachedToken {
  token: string;
  /** Epoch ms. Read from the JWT's own `exp`, not assumed. */
  expiresAt: number;
}
const tokenCache = new Map<string, CachedToken>();

/** Seconds of headroom: refresh this long before the token actually lapses,
 *  so a call that starts just under the wire does not finish just over it. */
const TOKEN_REFRESH_MARGIN_MS = 60_000;

/** Read `exp` out of a JWT without verifying it — this is a test helper
 *  deciding when to re-login, not an authorization check. A token whose exp
 *  cannot be read is treated as already expired, so the next call re-logins
 *  rather than sending something unusable. */
function jwtExpiryMs(token: string): number {
  try {
    const payload = token.split(".")[1];
    const json = JSON.parse(Buffer.from(payload, "base64url").toString("utf8"));
    return typeof json.exp === "number" ? json.exp * 1000 : 0;
  } catch {
    return 0;
  }
}

async function getAdminToken(): Promise<string> {
  const cached = tokenCache.get("paladin-admin");
  if (cached && Date.now() < cached.expiresAt - TOKEN_REFRESH_MARGIN_MS) {
    return cached.token;
  }
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
  tokenCache.set("paladin-admin", {
    token: access,
    expiresAt: jwtExpiryMs(access),
  });
  return access;
}

// Shared admin transport: one createConnectTransport call per
// helper would force a new fetch pool on every seed call. We
// build it once and reuse across TenantService / BucketService.
async function getDataToken(): Promise<string> {
  const cached = tokenCache.get("paladin-data");
  if (cached && Date.now() < cached.expiresAt - TOKEN_REFRESH_MARGIN_MS) {
    return cached.token;
  }
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
  tokenCache.set("paladin-data", {
    token: access,
    expiresAt: jwtExpiryMs(access),
  });
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
  if (cached && Date.now() < cached.expiresAt - TOKEN_REFRESH_MARGIN_MS) {
    return cached.token;
  }
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
  tokenCache.set("paladin-iam", {
    token: access,
    expiresAt: jwtExpiryMs(access),
  });
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

/**
 * Soft-delete an object through the data plane, by its canonical name.
 *
 * DeleteObject requires the OCC guard, so the current version is read first.
 * That is the same two-step the console does; passing no version used to be
 * accepted and skipped the concurrency check entirely.
 */
export async function softDeleteObject(name: string): Promise<void> {
  const client = createClient(ObjectService, dataTransport());
  const current = await client.getObject({ name });
  await client.deleteObject({ name, resourceVersion: current.resourceVersion });
}

/**
 * Upload a file large enough to require multipart, exercising the same
 * Initiate → PresignPart → PUT → Complete chain the console runs.
 *
 * Returns the object's resource name. A failure anywhere in the chain
 * throws rather than being papered over — the point is that the whole
 * sequence works, not that a row appeared.
 */
export async function seedMultipartObject(opts: {
  tenantId: string;
  collection: string;
  sizeBytes: number;
  key?: string;
}): Promise<{ name: string; key: string }> {
  const key = opts.key ?? `e2e/${uniqueSlug("big")}.bin`;
  const body = Buffer.alloc(opts.sizeBytes, "L");
  const client = createClient(MultipartUploadService, dataTransport());

  const init = await client.initiateMultipartUpload({
    parent: `tenants/${opts.tenantId}/collections/${opts.collection}`,
    key,
    contentType: "application/octet-stream",
    sizeBytes: BigInt(opts.sizeBytes),
    checksumAlgorithm: ChecksumAlgorithm.SHA256,
  });
  const objectName = init.object?.name ?? "";
  if (!objectName || !init.uploadId) {
    throw new Error("seed.ts: InitiateMultipartUpload returned no session");
  }

  const partSize = Number(init.recommendedPartSize);
  const partsTotal =
    init.totalParts || Math.max(1, Math.ceil(opts.sizeBytes / partSize));
  const parts: Array<{ partNumber: number; etag: string }> = [];

  for (let i = 0; i < partsTotal; i++) {
    const partNumber = i + 1;
    const signed = await client.presignPart({
      objectName,
      uploadId: init.uploadId,
      partNumber,
    });
    if (!signed.uploadUrl?.url) {
      throw new Error(`seed.ts: no presigned URL for part ${partNumber}`);
    }
    const slice = body.subarray(
      i * partSize,
      Math.min((i + 1) * partSize, opts.sizeBytes),
    );
    const res = await fetch(signed.uploadUrl.url, {
      method: signed.uploadUrl.method || "PUT",
      headers: { ...signed.uploadUrl.requiredHeaders },
      body: slice,
    });
    if (!res.ok) {
      throw new Error(`seed.ts: part ${partNumber} PUT failed: ${res.status}`);
    }
    const etag = (res.headers.get("etag") ?? "").replaceAll('"', "");
    if (!etag) {
      throw new Error(`seed.ts: part ${partNumber} returned no ETag`);
    }
    parts.push({ partNumber, etag });
  }

  await client.completeMultipartUpload({
    objectName,
    uploadId: init.uploadId,
    parts,
  });
  return { name: objectName, key };
}

// ─── Quota ──────────────────────────────────────────────────────────────────

export interface SeededQuota {
  name: string;
  resourceVersion: string;
}

/**
 * Set a tenant's quota through the admin API and return the version the write
 * produced.
 *
 * SetQuota is OCC-guarded in the upsert's DO UPDATE clause: "0" asserts no row
 * exists yet and is itself a conflict if one does, so this reads the current
 * version first rather than assuming a fresh tenant.
 */
export async function seedQuota(opts: {
  tenantId: string;
  maxTotalBytes?: bigint;
  maxObjectCount?: bigint;
}): Promise<SeededQuota> {
  const client = createClient(QuotaService, adminTransport());
  const name = `tenants/${opts.tenantId}/quota`;

  let resourceVersion = "0";
  try {
    const current = await client.getQuota({ name });
    resourceVersion = current.resourceVersion;
  } catch (err) {
    // NotFound is the expected shape for a tenant with no quota row — that is
    // what "0" is for. Anything else is a real failure and must not be
    // swallowed into a misleading create attempt.
    if (!(err instanceof ConnectError) || err.code !== Code.NotFound) throw err;
  }

  const res = await client.setQuota({
    name,
    resourceVersion,
    updateMask: {
      paths: ["max_total_bytes", "max_object_count"],
    },
    quota: {
      name,
      maxTotalBytes: opts.maxTotalBytes ?? BigInt(1_073_741_824),
      maxObjectCount: opts.maxObjectCount ?? BigInt(1000),
    },
  });
  return { name, resourceVersion: res.resourceVersion };
}

/** Read a quota's current resource_version, for tests that need to detect a
 *  write the UI made (or prove one was refused). */
export async function quotaVersion(tenantId: string): Promise<string> {
  const client = createClient(QuotaService, adminTransport());
  const q = await client.getQuota({ name: `tenants/${tenantId}/quota` });
  return q.resourceVersion;
}

/** Read a bucket's current settings + version, for tests that assert a UI
 *  write landed (or was refused). */
export async function bucketState(
  backendId: string,
  bucketId: string,
): Promise<{
  resourceVersion: string;
  versioningEnabled: boolean;
  keepDeletesForever: boolean;
}> {
  const b = await bucketAdminClient().getBucket({
    name: `storageBackends/${backendId}/buckets/${bucketId}`,
  });
  return {
    resourceVersion: b.resourceVersion,
    versioningEnabled: b.versioning?.enabled ?? false,
    keepDeletesForever: b.versioning?.keepDeletesForever ?? false,
  };
}

/** Flip a bucket's versioning through the admin API — used to move the
 *  resource_version underneath an open form. */
export async function setBucketVersioning(opts: {
  backendId: string;
  bucketId: string;
  enabled: boolean;
  keepDeletesForever?: boolean;
}): Promise<string> {
  const name = `storageBackends/${opts.backendId}/buckets/${opts.bucketId}`;
  const current = await bucketAdminClient().getBucket({ name });
  const res = await bucketAdminClient().setVersioning({
    name,
    resourceVersion: current.resourceVersion,
    versioning: {
      enabled: opts.enabled,
      keepDeletesForever: opts.keepDeletesForever ?? false,
    },
  });
  return res.resourceVersion;
}

/** Read a backend's operational flags + version. */
export async function backendState(backendId: string): Promise<{
  resourceVersion: string;
  enabled: boolean;
  readOnly: boolean;
  maintenance: boolean;
}> {
  const b = await backendAdminClient().getBackend({
    name: `storageBackends/${backendId}`,
  });
  return {
    resourceVersion: b.resourceVersion,
    enabled: b.enabled,
    readOnly: b.readOnly,
    maintenance: b.maintenance,
  };
}

/** Seed a backend that is enabled — the starting state for drain/maintenance
 *  tests, which seedDisabledBackend deliberately does not give. */
export async function seedEnabledBackend(): Promise<SeededBackend> {
  const be = await seedDisabledBackend();
  const current = await backendAdminClient().getBackend({
    name: `storageBackends/${be.backendId}`,
  });
  await backendAdminClient().setBackendEnabled({
    name: `storageBackends/${be.backendId}`,
    enabled: true,
    resourceVersion: current.resourceVersion,
  });
  return be;
}

/**
 * Kick off a BatchUpdateTags operation and return its name.
 *
 * The point is the row it leaves in `operations`: its metadata is a
 * google.protobuf.Any, which is what makes ListOperations exercise the Any
 * path at all. An empty operations table hides that entirely.
 */
export async function seedBatchTagOperation(opts: {
  tenantId: string;
  collection: string;
  objectName: string;
}): Promise<string> {
  const client = createClient(BatchService, dataTransport());
  const res = await client.batchUpdateTags({
    parent: `tenants/${opts.tenantId}/collections/${opts.collection}`,
    selector: { names: [opts.objectName] },
    tags: { env: "e2e" },
  });
  return res.name;
}

/** Read a bucket's object-lock config alongside its version. */
export async function bucketLockState(
  backendId: string,
  bucketId: string,
): Promise<{
  resourceVersion: string;
  enabled: boolean;
  defaultMode: string;
  defaultRetentionSeconds: number;
}> {
  const b = await bucketAdminClient().getBucket({
    name: `storageBackends/${backendId}/buckets/${bucketId}`,
  });
  return {
    resourceVersion: b.resourceVersion,
    enabled: b.objectLock?.enabled ?? false,
    defaultMode: String(b.objectLock?.defaultMode ?? ""),
    defaultRetentionSeconds: Number(
      b.objectLock?.defaultRetention?.seconds ?? 0,
    ),
  };
}

/** Read a bucket's lifecycle rules alongside its version. */
export async function bucketLifecycle(
  backendId: string,
  bucketId: string,
): Promise<{ resourceVersion: string; ruleCount: number }> {
  const b = await bucketAdminClient().getBucket({
    name: `storageBackends/${backendId}/buckets/${bucketId}`,
  });
  return {
    resourceVersion: b.resourceVersion,
    ruleCount: b.lifecycleRules?.length ?? 0,
  };
}

/** Move a bucket's version out from under an open form, by toggling a field
 *  the test is not itself editing. */
export async function bumpBucketVersion(
  backendId: string,
  bucketId: string,
): Promise<string> {
  const name = `storageBackends/${backendId}/buckets/${bucketId}`;
  const current = await bucketAdminClient().getBucket({ name });
  const res = await bucketAdminClient().updateBucket({
    name,
    resourceVersion: current.resourceVersion,
    updateMask: { paths: ["display_name"] },
    bucket: { displayName: `${current.displayName} (touched)` },
  });
  return res.resourceVersion;
}

// ─── Event subscriptions ────────────────────────────────────────────────────

/** Count a tenant's event subscriptions — the observable effect of the
 *  editor dialog's Create/Delete. */
export async function subscriptionCount(tenantId: string): Promise<number> {
  const client = createClient(EventSubscriptionService, adminTransport());
  const res = await client.listSubscriptions({ parent: `tenants/${tenantId}` });
  return res.subscriptions.length;
}

/** Create a subscription through the API, for tests that need one to exist
 *  before they drive the UI against it. */
export async function seedSubscription(opts: {
  tenantId: string;
  httpUrl?: string;
}): Promise<{ name: string; resourceVersion: string }> {
  const client = createClient(EventSubscriptionService, adminTransport());
  const res = await client.createSubscription({
    parent: `tenants/${opts.tenantId}`,
    subscription: {
      tenantId: opts.tenantId,
      sink: {
        target: {
          case: "http",
          value: { url: opts.httpUrl ?? "https://hooks.example.invalid/e2e" },
        },
      },
    },
  });
  return { name: res.name, resourceVersion: res.resourceVersion };
}

// ─── Tenant budget ──────────────────────────────────────────────────────────

/** Read a tenant's budget cap and spend. Returns null when no budget row
 *  exists — the state the console renders as "Create budget". */
export async function tenantBudget(tenantId: string): Promise<{
  maxBudgetAmount: number;
  spentAmount: number;
  unitCode: string;
  resourceVersion: string;
} | null> {
  const client = createClient(TenantBudgetService, adminTransport());
  try {
    const res = await client.get({ tenantId });
    return {
      maxBudgetAmount: res.budget?.maxBudgetAmount ?? 0,
      spentAmount: res.budget?.spentAmount ?? 0,
      unitCode: res.budget?.unitCode ?? "",
      resourceVersion: res.budget?.resourceVersion ?? "",
    };
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.NotFound) return null;
    throw err;
  }
}

// ─── Collections ────────────────────────────────────────────────────────────

/**
 * Count a tenant's collections, for asserting a create or delete landed.
 *
 * Pages through to the end. A single request capped at 200 returned exactly
 * 200 against a tenant that had 335, so before and after a delete were both
 * "200" and the assertion could never see the difference — a test that could
 * only fail, never pass, for a reason that had nothing to do with the delete.
 */
export async function collectionCount(tenantId: string): Promise<number> {
  const client = createClient(CollectionService, adminTransport());
  let total = 0;
  let pageToken = "";
  do {
    const res = await client.listCollections({
      parent: `tenants/${tenantId}`,
      page: { pageSize: 200, pageToken },
    });
    total += res.collections.length;
    pageToken = res.page?.nextPageToken ?? "";
  } while (pageToken);
  return total;
}

// ─── Teardown ───────────────────────────────────────────────────────────────

/**
 * Delete the tenants a test created.
 *
 * Nothing cleaned up before this, and it was not merely untidy: after a day of
 * runs the cluster held 500 tenants, every console page rendered hundreds of
 * rows, and clicks started being dropped while React worked through them. The
 * flake that looked like a viewport problem, then like cross-file
 * interference, was in large part this — tests slowly making the app they were
 * testing slower.
 *
 * Trash then purge, because those are the two transitions the API has —
 * `force` used to collapse them into one call and is gone. Failures are
 * swallowed: a teardown that throws turns a passing test red for a reason that
 * has nothing to do with what it asserted.
 */
/**
 * A teardown target that is already gone is a success, not a warning.
 *
 * A spec whose subject IS the delete (collections-crud deletes its own
 * collection, trash.spec trashes its own tenant) reaches teardown with nothing
 * left to remove. Reporting that trains the reader to skim past teardown
 * warnings, which is the opposite of why they exist.
 */
function alreadyGone(err: unknown): boolean {
  return err instanceof ConnectError && err.code === Code.NotFound;
}

/**
 * Delete the users a tenant owns, and any this test created by other means.
 *
 * `users.tenant_id` is ON DELETE RESTRICT, so a tenant that still has one
 * cannot be purged — the membership fixture creates exactly one, which is why
 * every `switch-*` tenant stopped at "trashed" and stayed in the list.
 *
 * The UI tests are the other half: user-admin.spec types a subject into the
 * console's create dialog, so no fixture ever learns the row exists. Those are
 * registered by subject and removed here.
 */
export async function deleteUsersOf(tenantId: string): Promise<void> {
  const client = createClient(UserService, iamAdminTransport());
  const page = await client.listUsers({
    parent: `tenants/${tenantId}`,
    page: { pageSize: 200, pageToken: "" },
  });
  await Promise.allSettled(
    page.users.map((u) =>
      client.deleteUser({ name: u.name, resourceVersion: u.resourceVersion }),
    ),
  );
}

/**
 * Delete users by subject, wherever they live. Used for rows a UI test
 * created, where the test knows only what it typed.
 */
export async function deleteUsersBySubject(subjects: string[]): Promise<void> {
  if (!subjects.length) return;
  const client = createClient(UserService, iamAdminTransport());
  const wanted = new Set(subjects);
  const results = await Promise.allSettled(
    subjects.map(async (subject) => {
      // Cross-tenant listing (empty parent) is platform-admin only, which the
      // fixture is; filtering server-side keeps this to one page.
      const page = await client.listUsers({
        parent: "",
        page: { pageSize: 50, pageToken: "" },
        filter: `subject == ${JSON.stringify(subject)}`,
      });
      for (const u of page.users) {
        if (!wanted.has(u.subject)) continue;
        await client.deleteUser({
          name: u.name,
          resourceVersion: u.resourceVersion,
        });
      }
    }),
  );
  for (const [i, r] of results.entries()) {
    if (r.status === "rejected" && !alreadyGone(r.reason)) {
      console.warn(
        `[teardown] user ${subjects[i]} not deleted: ${String(r.reason).slice(0, 200)}`,
      );
    }
  }
}

/**
 * Every collection a tenant owns, as deleteCollections takes them.
 *
 * Registration covers what a fixture created; this covers what the tenant
 * ended up with — a collection made through the console's own dialog counts
 * against the purge exactly the same way.
 */
/**
 * Read a Collection's stored Cedar policy and its current version.
 *
 * The policy editor's whole job is to make this string what the operator
 * typed, so a test that only asserts on the toast proves nothing: a save that
 * reported success and wrote the previous text would pass it. Reading the
 * stored value back through the admin API is the assertion that cannot be
 * satisfied by the UI alone.
 */
export async function collectionPolicy(
  tenantId: string,
  collection: string,
): Promise<{ cedarPolicy: string; resourceVersion: string }> {
  const client = createClient(CollectionService, adminTransport());
  const got = await client.getCollection({
    name: `tenants/${tenantId}/collections/${collection}`,
  });
  return {
    cedarPolicy: got.cedarPolicy,
    resourceVersion: got.resourceVersion,
  };
}

export async function collectionsOf(
  tenantId: string,
): Promise<SeededCollection[]> {
  const client = createClient(CollectionService, adminTransport());
  const out: SeededCollection[] = [];
  let pageToken = "";
  do {
    const res = await client.listCollections({
      parent: `tenants/${tenantId}`,
      page: { pageSize: 200, pageToken },
    });
    for (const c of res.collections) {
      out.push({
        tenantId,
        bucket: c.bucket,
        collection: c.collection,
        displayName: c.displayName,
      });
    }
    pageToken = res.page?.nextPageToken ?? "";
  } while (pageToken);
  return out;
}

export async function deleteTenants(tenantIds: string[]): Promise<void> {
  const client = createClient(TenantService, adminTransport());
  const results = await Promise.allSettled(
    tenantIds.map(async (id) => {
      // Soft-delete into the trash, then purge — the two transitions the API
      // offers, in the only order that works: PurgeTenant wants a trashed
      // tenant ("tenant is active; soft-delete it first"), and DeleteTenant
      // only ever trashes.
      //
      // The delete is allowed to fail: a spec that trashes its own fixture
      // (US5 does, that being the feature) leaves nothing for it to do, and
      // the purge afterwards is still the step that matters.
      try {
        const current = await client.getTenant({ name: `tenants/${id}` });
        await client.deleteTenant({
          name: `tenants/${id}`,
          resourceVersion: current.resourceVersion,
        });
      } catch {
        // Already trashed, or gone. The purge below decides which.
      }
      // Walk the RESTRICT edges the way docs/deletion-semantics.md prescribes:
      // objects, then collections, then users. Trashed was "enough" while
      // nothing walked them, and the result was a switch-* tenant left in the
      // trash after every run that used the membership fixture — its one
      // users row was all it took to refuse the purge.
      await deleteCollections(await collectionsOf(id)).catch(() => {});
      await deleteUsersOf(id).catch(() => {});
      // Purging is still not always reachable — an object under a COMPLIANCE
      // lock cannot be removed by anyone, and that is the product rule
      // working. Trashed is the honest fallback then.
      try {
        await client.purgeTenant({ name: `tenants/${id}` });
      } catch (err) {
        const msg = String(err);
        if (!/object keys or objects/.test(msg)) throw err;
      }
    }),
  );
  // A teardown that fails silently is how the cluster filled up in the first
  // place — the fixture reported success while the rows stayed. Say so. A
  // tenant that could only be trashed is not reported: that is the expected
  // outcome whenever the test wrote any data.
  for (const [i, r] of results.entries()) {
    if (r.status === "rejected" && !alreadyGone(r.reason)) {
      console.warn(
        `[teardown] tenant ${tenantIds[i]} not purged: ${String(r.reason).slice(0, 200)}`,
      );
    }
  }
}

/**
 * Set a tenant budget straight through the admin API, bypassing the console.
 *
 * Used to simulate the other operator in an OCC race: the page holds a version
 * it read, this moves the row underneath it.
 */
export async function setTenantBudget(args: {
  tenantId: string;
  maxBudgetAmount: number;
  resourceVersion: string;
  unitCode?: string;
  resetSpend?: boolean;
}): Promise<string> {
  const client = createClient(TenantBudgetService, adminTransport());
  const res = await client.set({
    tenantId: args.tenantId,
    maxBudgetAmount: args.maxBudgetAmount,
    unitCode: args.unitCode ?? "USD",
    resetSpend: args.resetSpend ?? false,
    resourceVersion: args.resourceVersion,
  });
  return res.budget?.resourceVersion ?? "";
}

/**
 * Permanently remove every object a collection holds.
 *
 * `objects.collection_id` is ON DELETE RESTRICT, so this is not an
 * optimisation — a collection that holds anything cannot be deleted at all.
 * Permanent, because a soft delete leaves the row in place and the FK does not
 * care that it is trashed.
 *
 * An object under a COMPLIANCE lock or a legal hold cannot be removed by
 * anyone, which is the product rule working; the collection then survives and
 * teardown says so rather than pretending.
 */
async function purgeObjectsIn(item: SeededCollection): Promise<void> {
  const client = createClient(ObjectService, dataTransport());
  const parent = `tenants/${item.tenantId}/collections/${item.collection}`;
  // A page at a time, deleting as we go: the cursor is over rows we are
  // removing, so re-reading the first page until it comes back empty is both
  // simpler and correct, where paging forward would skip.
  for (let round = 0; round < 20; round++) {
    const page = await client.listObjects({
      parent,
      page: { pageSize: 200, pageToken: "" },
    });
    if (!page.objects.length) return;
    await Promise.allSettled(
      page.objects.map((o) =>
        client.deleteObject({
          name: o.name,
          resourceVersion: o.resourceVersion,
          permanent: true,
          // Only honoured against a GOVERNANCE window, and only because the
          // fixture runs as platform.admin. COMPLIANCE and legal hold stay
          // absolute, as they should.
          bypassGovernanceRetention: true,
        }),
      ),
    );
  }
}

/**
 * Delete collections, and the objects that keep them alive.
 *
 * Best-effort per collection: one that still holds an undeletable object
 * refuses, and a teardown must not fail a test that passed. What it must not
 * do is fail silently — that is how the cluster filled up while every run
 * reported success.
 */
export async function deleteCollections(
  items: SeededCollection[],
): Promise<void> {
  const client = collectionAdminClient();
  const results = await Promise.allSettled(
    items.map(async (item) => {
      await purgeObjectsIn(item);
      const name = `tenants/${item.tenantId}/collections/${item.collection}`;
      const current = await client.getCollection({ name });
      await client.deleteCollection({
        name,
        resourceVersion: current.resourceVersion,
      });
    }),
  );
  for (const [i, r] of results.entries()) {
    if (r.status === "rejected" && !alreadyGone(r.reason)) {
      console.warn(
        `[teardown] collection ${items[i].collection} not deleted: ` +
          `${String(r.reason).slice(0, 200)}`,
      );
    }
  }
}

/**
 * Delete buckets by (backend, name).
 *
 * `delete_on_backend` stays false: the physical bucket in the test stack is
 * shared and provisioning is the worker's job, which this stack does not run.
 * A bucket still referenced by a collection refuses — collections->buckets is
 * RESTRICT too — so this runs after deleteCollections, never before.
 */
export async function deleteBuckets(items: SeededBucket[]): Promise<void> {
  const client = bucketAdminClient();
  const results = await Promise.allSettled(
    items.map(async (b) => {
      const name = `storageBackends/${b.backendId}/buckets/${b.bucketId}`;
      const current = await client.getBucket({ name });
      await client.deleteBucket({
        name,
        resourceVersion: current.resourceVersion,
        deleteOnBackend: false,
      });
    }),
  );
  for (const [i, r] of results.entries()) {
    if (r.status === "rejected" && !alreadyGone(r.reason)) {
      console.warn(
        `[teardown] bucket ${items[i].bucketId} not deleted: ` +
          `${String(r.reason).slice(0, 200)}`,
      );
    }
  }
}

/**
 * Delete storage backends by id. Best-effort per backend: one that has since
 * acquired buckets refuses deletion, and a teardown must not fail a test that
 * passed.
 */
export async function deleteBackends(backendIds: string[]): Promise<void> {
  const client = backendAdminClient();
  await Promise.allSettled(
    backendIds.map(async (id) => {
      const current = await client.getBackend({
        name: `storageBackends/${id}`,
      });
      await client.deleteBackend({
        name: `storageBackends/${id}`,
        resourceVersion: current.resourceVersion,
      });
    }),
  );
}
