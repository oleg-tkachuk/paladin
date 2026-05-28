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
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { AuthService } from "@/gen/paladin/iam/v1/auth_service_pb";
import { TenantService } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { BackendService } from "@/gen/paladin/admin/v1/backend_service_pb";
import { StorageKind } from "@/gen/paladin/admin/v1/types_pb";
import { BucketService } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { ObjectKeyService } from "@/gen/paladin/admin/v1/object_key_service_pb";
import {
  CapabilityService,
  PrincipalKind,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import { SEEDED_ADMIN } from "./credentials";
import { uniqueSlug, uniqueDisplayName } from "./unique";

// ─── plane base URLs (test stack) ──────────────────────────
const IAM_URL = process.env.PALADIN_E2E_IAM_URL ?? "http://localhost:8085";
const ADMIN_URL = process.env.PALADIN_E2E_ADMIN_URL ?? "http://localhost:8090";

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
    createConnectTransport({ baseUrl: IAM_URL }),
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
function adminTransport() {
  return createConnectTransport({
    baseUrl: ADMIN_URL,
    interceptors: [
      (next) => async (req) => {
        const token = await getAdminToken();
        req.header.set("Authorization", `Bearer ${token}`);
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

function objectKeyAdminClient() {
  return createClient(ObjectKeyService, adminTransport());
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
      $typeName: "paladin.admin.v1.Tenant",
      tenantId: "",
      name: "",
      slug,
      displayName,
      inheritedCedarPolicy: "",
      labels: {},
      resourceVersion: "",
    } as never,
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
  bucketName: string;
  /** Human-facing display name. UUID-suffixed. */
  displayName: string;
}

/**
 * Create a bucket via BucketService.CreateBucket under the
 * given backend. Defaults to the chart-default `primary`
 * backend the bootstrap container provisions.
 *
 * Note `provisionOnBackend: false` — for the e2e suite we
 * don't actually need the bucket to exist on the Garage
 * side. Every scope-picker test asserts metadata, not S3
 * I/O. Skipping provision shaves ~1s per seedBucket call.
 */
export async function seedBucket(opts?: {
  backendId?: string;
  bucketNamePrefix?: string;
  displayNamePrefix?: string;
}): Promise<SeededBucket> {
  const backendId = opts?.backendId ?? "primary";
  const bucketName = uniqueSlug(opts?.bucketNamePrefix ?? "e2e-bucket");
  const displayName = uniqueDisplayName(
    opts?.displayNamePrefix ?? "E2E Bucket",
  );
  const client = bucketAdminClient();
  await client.createBucket({
    parent: `storageBackends/${backendId}`,
    bucketName,
    bucket: {
      $typeName: "paladin.admin.v1.Bucket",
      backendId,
      bucketName,
      displayName,
      region: "",
      labels: {},
      cedarPolicy: "",
    } as never,
    provisionOnBackend: false,
  });
  return { backendId, bucketName, displayName };
}

// ─── object_key seeding ────────────────────────────────────

export interface SeededObjectKey {
  tenantId: string;
  /** Bucket resource name: `storageBackends/{backend}/buckets/{name}`. */
  bucket: string;
  /** ObjectKey identifier — `e2e/<8-hex>`. */
  objectKey: string;
  displayName: string;
}

/**
 * Create an ObjectKey under the given (tenant, bucket) via
 * ObjectKeyService.CreateObjectKey. The key path is
 * `e2e/<8-hex>` so concurrent tests don't collide on the
 * (tenant_id, object_key) UNIQUE constraint.
 */
export async function seedObjectKey(opts: {
  tenantId: string;
  bucket: SeededBucket;
  objectKeyPrefix?: string;
}): Promise<SeededObjectKey> {
  const objectKey = `e2e/${uniqueSlug(opts.objectKeyPrefix ?? "key").replace(/^[^-]+-/, "")}`;
  const displayName = uniqueDisplayName("E2E Key");
  const bucketResourceName = `storageBackends/${opts.bucket.backendId}/buckets/${opts.bucket.bucketName}`;
  const client = objectKeyAdminClient();
  await client.createObjectKey({
    parent: `tenants/${opts.tenantId}`,
    objectKey,
    objectKeyResource: {
      $typeName: "paladin.admin.v1.ObjectKey",
      name: "",
      tenantId: opts.tenantId,
      objectKey,
      displayName,
      bucket: bucketResourceName,
      cedarPolicy: "",
    } as never,
  });
  return {
    tenantId: opts.tenantId,
    bucket: bucketResourceName,
    objectKey,
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
      $typeName: "paladin.admin.v1.CapabilityPrincipal",
      kind: PrincipalKind.AGENT,
      tenantId: opts.tenantId,
      subject,
    } as never,
    audience: ["paladin-data"],
    caveats: {
      $typeName: "paladin.admin.v1.CapabilityCaveats",
      ops: ["get"],
      resourcePrefixes: [],
      resourceUris: [],
      maxRequests: BigInt(100),
      maxBudgetAmount: BigInt(0),
      unitCode: "",
      allowTaintedRead: false,
      idempotencyKeyRequired: false,
      sourceIpCidr: [],
    } as never,
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
