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

function adminClient() {
  return createClient(
    TenantService,
    createConnectTransport({
      baseUrl: ADMIN_URL,
      interceptors: [
        (next) => async (req) => {
          const token = await getAdminToken();
          req.header.set("Authorization", `Bearer ${token}`);
          return next(req);
        },
      ],
    }),
  );
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
  const client = adminClient();
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

// seedBucket / seedObjectKey arrive in Phase 5 (US3) — see
// tasks.md T024. They're not used by US1 or US2.
