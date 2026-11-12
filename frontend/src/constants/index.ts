/**
 * API contract constants — single source of truth shared across hooks,
 * pages, and the RPC bridge.
 */

/** Where an operator learns how to turn capabilities on; they are off by default. */
export const CAPABILITIES_DOCS_URL =
  "https://github.com/oleg-tkachuk/paladin/blob/main/docs/install.md#capabilities";

/** Default page size for list-style RPCs. */
export const API_LIMIT_DEFAULT = 20;

/** Maximum page size accepted by the backend `*.List*` validators. */
export const API_PAGE_SIZE_MAX = 500;

/**
 * The console's session role (ADR-0048), on the console's own origin: the
 * ingress routes SESSION_PREFIX to it. It holds the session cookie and the
 * tokens behind it, and forwards SESSION_RPC_PREFIX/<package.Service>/<Method>
 * to the plane that serves it.
 */
export const SESSION_PREFIX = "/session";
export const SESSION_RPC_PREFIX = `${SESSION_PREFIX}/rpc`;
export const SESSION_PATHS = {
  login: `${SESSION_PREFIX}/login`,
  logout: `${SESSION_PREFIX}/logout`,
  refresh: `${SESSION_PREFIX}/refresh`,
  switchTenant: `${SESSION_PREFIX}/switch-tenant`,
  auditStream: `${SESSION_PREFIX}/audit/stream`,
} as const;

/** Where the console serves the operator's banner, its own configuration. */
export const BANNER_PATH = "/api/banner";

/** The session cookie the session role sets; the page itself never reads it. */
export const SESSION_COOKIE = "__Host-paladin_session";

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
  /** Active Collection scope for /objects + sidebar counts + CommandPalette. */
  scopeCollection: "paladin_scope_object_key",
  /** Saved filter views on /objects. */
  savedViews: "paladin_saved_views",
} as const;

/**
 * The product the console is named by in a plane's audit log: the session
 * role's User-Agent (session.ConsoleUserAgentProduct in the backend).
 */
export const CONSOLE_USER_AGENT_PRODUCT = "paladin-console";

/** How long a query's data counts as fresh unless the query says otherwise. */
export const QUERY_STALE_MS = 30_000;
