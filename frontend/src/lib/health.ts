// The health page's model: the wire format of backend/internal/health
// snapshots, as the BFF's /api/health/all returns them, and the database
// view built from them.

// A role is healthy, degraded or unhealthy; a component can also be disabled,
// switched off, which is never probed and never counts toward a role.
export type RoleStatus = "healthy" | "degraded" | "unhealthy";
export type ComponentStatus = RoleStatus | "disabled";
// Where a component's on/off switch lives — backend/internal/health.Control.
export type Control = "always_on" | "config" | "database";

export type Detail = {
  name: string;
  value: string;
  // The role a detail is about, when a merged component carries several.
  role?: string;
};

export type Component = {
  name: string;
  status: ComponentStatus;
  message?: string;
  latency_ms: number;
  category: string;
  critical: boolean;
  control?: Control;
  // Facts the component reports beside its status, in its order.
  details?: Detail[];
  // Shown in place of the status's own label.
  statusLabel?: string;
};

export type Snapshot = {
  role: string;
  status: RoleStatus;
  components: Component[];
  checked_at: string;
};

export type HealthAll = { roles: Snapshot[] };

// The category every role's database components carry, and the component
// that says whether the role reaches the database at all.
export const DATABASE_CATEGORY = "database";
export const DATABASE_PRIMARY = "postgres";
// The card the database components are gathered into.
export const DATABASE_CARD = "database";

// Each role reports the database from its own pod — readiness is per pod —
// so every role carried the same schema version and replica state, and its
// own pool. splitDatabase gathers them into one database card and leaves
// each role card one row: whether that role reaches the database.
export function splitDatabase(roles: Snapshot[]): {
  roles: Snapshot[];
  database: Snapshot | null;
} {
  const byName = new Map<string, { role: string; c: Component }[]>();
  const outRoles = roles.map((snap) => {
    const db = snap.components.filter((c) => c.category === DATABASE_CATEGORY);
    if (db.length === 0) return snap;
    for (const c of db) {
      const seen = byName.get(c.name) ?? [];
      seen.push({ role: snap.role, c });
      byName.set(c.name, seen);
    }
    return { ...snap, components: roleComponents(snap.components, db) };
  });
  if (byName.size === 0) return { roles, database: null };

  const components = Array.from(byName.values(), merge);
  return {
    roles: outRoles,
    database: {
      role: DATABASE_CARD,
      status: rollup(components),
      components,
      checked_at: latest(roles),
    },
  };
}

// roleComponents keeps a role's own components and puts one connectivity
// row where its database components were.
function roleComponents(all: Component[], db: Component[]): Component[] {
  const primary = db.find((c) => c.name === DATABASE_PRIMARY);
  const out: Component[] = [];
  let placed = false;
  for (const c of all) {
    if (c.category !== DATABASE_CATEGORY) {
      out.push(c);
      continue;
    }
    if (placed || !primary) continue;
    placed = true;
    out.push(connectivity(primary, db));
  }
  return out;
}

// connectivity is the role's database row: online or offline, and, when
// another of the role's database checks fails, where to look.
function connectivity(primary: Component, db: Component[]): Component {
  const failing = db
    .filter((c) => c !== primary && c.status === "unhealthy")
    .map((c) => c.name);
  return {
    ...primary,
    details: undefined,
    statusLabel: primary.status === "healthy" ? "Online" : "Offline",
    message:
      primary.status === "healthy"
        ? failing.length > 0
          ? `${failing.join(", ")} failing: see ${DATABASE_CARD}`
          : undefined
        : primary.message,
  };
}

const SEVERITY: Record<ComponentStatus, number> = {
  disabled: 0,
  healthy: 1,
  degraded: 2,
  unhealthy: 3,
};

// merge folds one database component as each role reported it: the worst
// status; a message or details said once when every role says the same, and
// per role when they differ — a pool is each role's own.
function merge(seen: { role: string; c: Component }[]): Component {
  const first = seen[0].c;
  const worst = seen.reduce(
    (w, { c }) => (SEVERITY[c.status] > SEVERITY[w.status] ? c : w),
    first,
  );

  const messages = seen.filter(({ c }) => c.message);
  const sameMessage = messages.every(
    ({ c }) => c.message === messages[0]?.c.message,
  );
  const message =
    messages.length === 0
      ? undefined
      : sameMessage
        ? messages[0].c.message
        : messages.map(({ role, c }) => `${role}: ${c.message}`).join("\n");

  const key = (ds?: Detail[]) => JSON.stringify(ds ?? []);
  const sameDetails = seen.every(
    ({ c }) => key(c.details) === key(first.details),
  );
  const details = sameDetails
    ? first.details
    : seen.flatMap(({ role, c }) =>
        (c.details ?? []).map((d) => ({ ...d, role })),
      );

  return {
    ...first,
    status: worst.status,
    critical: seen.some(({ c }) => c.critical),
    latency_ms: Math.max(...seen.map(({ c }) => c.latency_ms)),
    message,
    details: details?.length ? details : undefined,
  };
}

// rollup is the backend's rule for a role: a critical unhealthy component
// makes it unhealthy, any other unhealthy one degraded.
function rollup(components: Component[]): RoleStatus {
  let worst: RoleStatus = "healthy";
  for (const c of components) {
    if (c.status === "unhealthy" && c.critical) return "unhealthy";
    if (c.status === "unhealthy" || c.status === "degraded") worst = "degraded";
  }
  return worst;
}

function latest(roles: Snapshot[]): string {
  return (
    roles
      .map((r) => r.checked_at)
      .sort()
      .at(-1) ?? ""
  );
}
