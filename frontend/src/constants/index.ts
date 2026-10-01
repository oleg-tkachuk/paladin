/**
 * API contract constants — single source of truth shared across hooks,
 * pages, and the RPC bridge.
 */

/** Default page size for list-style RPCs. */
/** Where an operator learns how to turn capabilities on; they are off by default. */
export const CAPABILITIES_DOCS_URL =
  "https://github.com/oleg-tkachuk/paladin/blob/main/docs/install.md#capabilities";

export const API_LIMIT_DEFAULT = 20;

/** Maximum page size accepted by the backend `*.List*` validators. */
export const API_PAGE_SIZE_MAX = 500;

/** Hierarchy separator used in collections / path display. */
export const CATEGORY_SEPARATOR = "/";

/** Prefix where the Next.js Connect-RPC bridge handles backend traffic. */
export const RPC_API_PREFIX = "/api/rpc";

/**
 * Per-plane sub-prefixes under RPC_API_PREFIX. The bridge dispatches to
 * data:8080 / iam:8085 / admin:8090 based on which sub-prefix the request
 * came in under. Browsers always hit `${RPC_API_PREFIX}${RPC_PLANE_PREFIXES.x}`.
 */
export const RPC_PLANE_PREFIXES = {
  data: "/data",
  iam: "/iam",
  admin: "/admin",
} as const;

export type Plane = keyof typeof RPC_PLANE_PREFIXES;

/**
 * Backend JWT audiences — must match `RequireAudience` in
 * paladin/auth/audience.go. One access token per audience;
 * IAM mints them via Login + ExchangeAudience.
 */
export const AUDIENCES = {
  data: "paladin-data",
  iam: "paladin-iam",
  admin: "paladin-admin",
} as const;

export type Audience = (typeof AUDIENCES)[Plane];

/**
 * Bootstrap-provisioned Collection. Used as the last-resort fallback when
 * no scope has been chosen yet (fresh tenant, first-ever page load).
 * dev-bootstrap.sh creates a Collection with this slug; production
 * tenants must opt out of this default by selecting a different key.
 */
export const DEFAULT_OBJECT_KEY = "default";

/**
 * localStorage keys — collected here so renames don't drift across
 * call sites and so we never collide with another app on the same
 * origin (use the `paladin_` prefix consistently).
 */
export const STORAGE_KEYS = {
  /**
   * Legacy single-audience token slot. Reserved for the /config dev override —
   * if set, it's used for ALL audiences as a bypass until real Login lands.
   * Real per-audience tokens live in memory (tokenStore), not localStorage.
   */
  authToken: "paladin_token",
  /** Active backend ID for soft-scope filters (set in TenantSwitcher). */
  scopeBackend: "paladin_scope_backend",
  /** Active bucket name for soft-scope filters. */
  scopeBucket: "paladin_scope_bucket",
  /** Active Collection scope for /objects + sidebar counts + CommandPalette. */
  scopeCollection: "paladin_scope_object_key",
  /** Saved filter views on /objects. */
  savedViews: "paladin_saved_views",
} as const;
