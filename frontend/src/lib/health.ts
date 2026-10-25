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
  // The thing a detail is about, when a component reports several facts of
  // it: the console shows a group on one line.
  group?: string;
  // The role a detail is about, when a merged component carries several.
  role?: string;
};

/** One line of a component's details: a group's facts, or one loose fact. */
export type DetailLine = {
  label: string;
  facts: { name: string; value: string }[];
};

/**
 * A component's details as lines, in the order they came: the facts of one
 * group on one line, under the group's name, and each loose fact on its own,
 * under its name.
 */
export function detailLines(details: readonly Detail[]): DetailLine[] {
  const lines: DetailLine[] = [];
  const byGroup = new Map<string, DetailLine>();
  for (const d of details) {
    if (!d.group) {
      lines.push({ label: d.name, facts: [{ name: "", value: d.value }] });
      continue;
    }
    let line = byGroup.get(d.group);
    if (!line) {
      line = { label: d.group, facts: [] };
      byGroup.set(d.group, line);
      lines.push(line);
    }
    line.facts.push({ name: d.name, value: d.value });
  }
  return lines;
}

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

/** A component that needs an operator, and the card it is on. */
export interface Finding {
  role: string;
  component: Component;
}

/**
 * Every component across the cards that is unhealthy or degraded, the worst
 * first and the required ones first among them, then in the cards' order: what
 * the page leads with, so a fault is not found by scrolling through cards.
 */
export function needsAttention(cards: readonly Snapshot[]): Finding[] {
  const out = cards.flatMap((s) =>
    s.components
      .filter((c) => c.status === "unhealthy" || c.status === "degraded")
      .map((component) => ({ role: s.role, component })),
  );
  // A stable sort keeps the cards' order within one severity.
  return out.sort(
    (a, b) =>
      SEVERITY[b.component.status] - SEVERITY[a.component.status] ||
      Number(b.component.critical) - Number(a.component.critical),
  );
}

/** How many components are in each state. */
export function countByStatus(
  components: readonly Component[],
): Record<ComponentStatus, number> {
  const out: Record<ComponentStatus, number> = {
    healthy: 0,
    degraded: 0,
    unhealthy: 0,
    disabled: 0,
  };
  for (const c of components) out[c.status]++;
  return out;
}
