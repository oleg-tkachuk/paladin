// The g-prefix jumps: press NAV_PREFIX, then a key within
// NAV_CHORD_WINDOW_MS. One table serves the handler (useGlobalShortcuts),
// the labels in the command palette and the keyboard help, so the three
// cannot disagree about where a key goes.

/** The key that arms a jump. Only the unshifted key: "G" does nothing. */
export const NAV_PREFIX = "g";
/** How long after the prefix the second key still counts as a jump. */
export const NAV_CHORD_WINDOW_MS = 800;
/** Separates the keys of a chord label; the palette splits on it. */
export const CHORD_KEY_SEPARATOR = " ";

interface NavJump {
  /** The second key of the chord. */
  key: string;
  /** What the jump does, as the keyboard help shows it. */
  label: string;
  /** Destination with no tenant in scope. */
  path: string;
  /** Destination while a tenant is in scope, built from its base path. */
  scoped?: (tenantBase: string) => string;
}

export const NAV_JUMPS: readonly NavJump[] = [
  { key: "d", label: "Go to dashboard", path: "/" },
  {
    key: "t",
    label: "Go to tenants (the tenant in scope, if any)",
    path: "/tenants",
    scoped: (base) => base,
  },
  {
    key: "b",
    label: "Go to buckets (of the tenant in scope, if any)",
    path: "/buckets",
    scoped: (base) => `${base}/buckets`,
  },
  {
    key: "k",
    label: "Go to collections (of the tenant in scope, if any)",
    path: "/collections",
    scoped: (base) => `${base}/collections`,
  },
  { key: "s", label: "Go to storage backends", path: "/storage-backends" },
  { key: "p", label: "Go to policies", path: "/policies" },
  { key: "a", label: "Go to audit log", path: "/audit" },
  { key: "u", label: "Go to users", path: "/users" },
  { key: "h", label: "Go to health", path: "/health" },
  { key: "c", label: "Go to configuration", path: "/config" },
];

/** The scoped tenant's route, slug first; null with no tenant in scope. */
export function tenantBasePath(
  tenant: { slug?: string; tenantId?: string } | null | undefined,
): string | null {
  const handle = tenant?.slug || tenant?.tenantId;
  return handle ? `/tenants/${encodeURIComponent(handle)}` : null;
}

/** Where `key` jumps to in the given scope; null when it is not a jump. */
export function jumpTarget(
  key: string,
  tenantBase: string | null,
): string | null {
  const jump = NAV_JUMPS.find((j) => j.key === key);
  if (!jump) return null;
  return tenantBase && jump.scoped ? jump.scoped(tenantBase) : jump.path;
}

/** The chord as the palette shows it ("G B"), or undefined when none lands on `href`. */
export function chordFor(
  href: string,
  tenantBase: string | null,
): string | undefined {
  const jump = NAV_JUMPS.find((j) => jumpTarget(j.key, tenantBase) === href);
  return jump
    ? [NAV_PREFIX, jump.key].join(CHORD_KEY_SEPARATOR).toUpperCase()
    : undefined;
}
