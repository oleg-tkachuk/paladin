import { describe, expect, it } from "vitest";

import {
  detailLines,
  detailMatrix,
  roleMatrix,
  selectedCard,
  type Detail,
  countByStatus,
  needsAttention,
  splitDatabase,
  type Component,
  type Snapshot,
} from "./health";

const at = "2026-10-08T18:00:00Z";
const comp = (name: string, extra: Partial<Component> = {}): Component => ({
  name,
  status: "healthy",
  latency_ms: 1,
  category: "database",
  critical: true,
  ...extra,
});
const role = (name: string, components: Component[]): Snapshot => ({
  role: name,
  status: "healthy",
  components,
  checked_at: at,
});

describe("splitDatabase", () => {
  it("leaves roles alone when none reports the database", () => {
    const roles = [role("mcp", [comp("process", { category: "subsystem" })])];
    expect(splitDatabase(roles)).toEqual({ roles, database: null });
  });

  it("takes the worst status, and the database card follows the backend rule", () => {
    const { database } = splitDatabase([
      role("api", [comp("postgres-schema")]),
      role("worker", [
        comp("postgres-schema", {
          status: "unhealthy",
          message: "schema at 55, this build needs 56",
        }),
      ]),
    ]);
    expect(database!.components[0].status).toBe("unhealthy");
    expect(database!.components[0].message).toBe(
      "schema at 55, this build needs 56",
    );
    expect(database!.status).toBe("unhealthy");
  });

  it("degrades, not fails, the card on a non-critical failure", () => {
    const { database } = splitDatabase([
      role("api", [
        comp("postgres-replica", {
          critical: false,
          status: "unhealthy",
          message: "lag",
        }),
      ]),
    ]);
    expect(database!.status).toBe("degraded");
  });

  it("names each role when their messages differ", () => {
    const { database } = splitDatabase([
      role("api", [
        comp("postgres", { status: "unhealthy", message: "refused" }),
      ]),
      role("worker", [
        comp("postgres", { status: "unhealthy", message: "timeout" }),
      ]),
    ]);
    expect(database!.components[0].message).toBe(
      "api: refused\nworker: timeout",
    );
  });

  it("says details once when the same, per role when not", () => {
    const same = [{ name: "schema applied", value: "56" }];
    const { database } = splitDatabase([
      role("api", [
        comp("postgres", { details: [{ name: "connections", value: "3" }] }),
        comp("postgres-schema", { details: same }),
      ]),
      role("worker", [
        comp("postgres", { details: [{ name: "connections", value: "4" }] }),
        comp("postgres-schema", { details: same }),
      ]),
    ]);
    const [pg, schema] = database!.components;
    expect(schema.details).toEqual(same);
    expect(pg.details).toEqual([
      { name: "connections", value: "3", role: "api" },
      { name: "connections", value: "4", role: "worker" },
    ]);
  });

  it("keeps a role's own components and one connectivity row in place of its database ones", () => {
    const { roles } = splitDatabase([
      role("dispatcher", [
        comp("postgres", { details: [{ name: "connections", value: "3" }] }),
        comp("postgres-schema"),
        comp("postgres-replica", { status: "disabled" }),
        comp("outbox", { category: "subsystem" }),
      ]),
    ]);
    const rows = roles[0].components;
    expect(rows.map((c) => c.name)).toEqual(["postgres", "outbox"]);
    expect(rows[0].statusLabel).toBe("Online");
    expect(rows[0].details).toBeUndefined();
  });

  it("reads offline with the role's own error when it cannot reach the database", () => {
    const { roles } = splitDatabase([
      role("api", [
        comp("postgres", {
          status: "unhealthy",
          message: "connection refused",
        }),
      ]),
    ]);
    expect(roles[0].components[0]).toMatchObject({
      statusLabel: "Offline",
      message: "connection refused",
    });
  });

  // A role can reach the database and still fail one of its checks — a
  // schema behind the build. Its card's status says unhealthy; the row says
  // where to look rather than leaving every visible row green.
  it("points a role card at the database card when a folded check fails", () => {
    const { roles } = splitDatabase([
      role("api", [
        comp("postgres"),
        comp("postgres-schema", { status: "unhealthy" }),
      ]),
    ]);
    expect(roles[0].components[0]).toMatchObject({
      statusLabel: "Online",
      message: "postgres-schema failing: see database",
    });
  });

  // Valkey, where the rate limits count (ADR-0046), is a datastore beside
  // postgres: one row on the database card, each role's view of it apart —
  // whether it counts shared or per process is that role's own.
  it("gathers valkey onto the database card, per role where roles differ", () => {
    const valkey = (counted: string, extra: Partial<Component> = {}) =>
      comp("valkey", {
        critical: false,
        details: [{ name: "rate limits counted", value: counted }],
        ...extra,
      });
    const { roles, database } = splitDatabase([
      role("api", [comp("postgres"), valkey("shared")]),
      role("admin", [
        comp("postgres"),
        valkey("per process", { status: "unhealthy", message: "i/o timeout" }),
      ]),
    ]);
    const row = database!.components.find((c) => c.name === "valkey")!;
    expect(row).toMatchObject({ status: "unhealthy", critical: false });
    expect(row.details).toEqual([
      { name: "rate limits counted", value: "shared", role: "api" },
      { name: "rate limits counted", value: "per process", role: "admin" },
    ]);
    expect(database!.status).toBe("degraded");
    expect(roles[1].components).toEqual([
      expect.objectContaining({
        name: "postgres",
        statusLabel: "Online",
        message: "valkey failing: see database",
      }),
    ]);
  });
});

describe("needsAttention", () => {
  const comp = (
    name: string,
    status: Component["status"],
    critical = false,
  ): Component => ({
    name,
    status,
    critical,
    latency_ms: 0,
    category: "",
  });
  const snap = (role: string, components: Component[]): Snapshot => ({
    role,
    status: "healthy",
    components,
    checked_at: "",
  });

  it("leads with what is down, the required first, then what is slow", () => {
    const findings = needsAttention([
      snap("api", [comp("cache", "degraded"), comp("ok", "healthy")]),
      snap("worker", [comp("nats", "unhealthy"), comp("off", "disabled")]),
      snap("admin", [comp("postgres", "unhealthy", true)]),
    ]);
    expect(findings.map((f) => `${f.role}/${f.component.name}`)).toEqual([
      "admin/postgres",
      "worker/nats",
      "api/cache",
    ]);
  });

  it("finds nothing when every check is healthy or switched off", () => {
    expect(
      needsAttention([
        snap("api", [comp("a", "healthy"), comp("b", "disabled")]),
      ]),
    ).toEqual([]);
  });
});

describe("countByStatus", () => {
  it("counts each state, the disabled apart", () => {
    const c = (status: Component["status"]): Component => ({
      name: status,
      status,
      critical: false,
      latency_ms: 0,
      category: "",
    });
    expect(
      countByStatus([c("healthy"), c("healthy"), c("disabled"), c("degraded")]),
    ).toEqual({
      healthy: 2,
      degraded: 1,
      unhealthy: 0,
      disabled: 1,
    });
  });
});

describe("detailLines", () => {
  it("puts a group's facts on one line, loose facts each on their own, in order", () => {
    expect(
      detailLines([
        { name: "in use", value: "3/20" },
        { group: "watcher", name: "connected", value: "1" },
        { name: "idle", value: "2" },
        { group: "watcher", name: "lost", value: "0" },
      ]),
    ).toEqual([
      { label: "in use", facts: [{ name: "", value: "3/20" }] },
      {
        label: "watcher",
        facts: [
          { name: "connected", value: "1" },
          { name: "lost", value: "0" },
        ],
      },
      { label: "idle", facts: [{ name: "", value: "2" }] },
    ]);
  });
});

// The details as the backend's producers write them (worker/ticklog.go,
// mcp/health.go, app/build_listeners_common.go, worker/dispatcher_health.go).
const job = (name: string, ran: boolean): Detail[] =>
  ran
    ? [
        { group: name, name: "every", value: "1h0m0s" },
        { group: name, name: "last run", value: "30m24s ago" },
        { group: name, name: "took", value: "139ms" },
        { group: name, name: "outcome", value: "ok" },
      ]
    : [
        { group: name, name: "every", value: "1h0m0s" },
        { group: name, name: "last run", value: "not yet" },
      ];

describe("detailMatrix", () => {
  it("sets out groups that share their facts as a row each, a fact to a column", () => {
    const m = detailMatrix([
      ...job("api_token_purger", true),
      ...job("lifecycle", false),
    ]);
    expect(m).toEqual({
      columns: ["every", "last run", "took", "outcome"],
      rows: [
        {
          label: "api_token_purger",
          cells: ["1h0m0s", "30m24s ago", "139ms", "ok"],
        },
        {
          label: "lifecycle",
          cells: ["1h0m0s", "not yet", undefined, undefined],
        },
      ],
    });
  });

  it("takes its columns from the group that reports most, wherever it stands", () => {
    const m = detailMatrix([...job("a", false), ...job("b", true)]);
    expect(m?.columns).toEqual(["every", "last run", "took", "outcome"]);
  });

  it("keeps a fact with no name as a column of its own", () => {
    const upstream = (name: string): Detail[] => [
      { group: name, name: "", value: "up" },
      { group: name, name: "in", value: "4ms" },
    ];
    expect(detailMatrix([...upstream("api"), ...upstream("admin")])).toEqual({
      columns: ["", "in"],
      rows: [
        { label: "api", cells: ["up", "4ms"] },
        { label: "admin", cells: ["up", "4ms"] },
      ],
    });
  });

  it("is none for groups that report different facts", () => {
    expect(
      detailMatrix([
        { group: "watcher", name: "connected", value: "3" },
        { group: "watcher", name: "lost", value: "0" },
        { group: "notifications", name: "received", value: "9" },
        { group: "clears", name: "narrow", value: "on" },
      ]),
    ).toBeNull();
  });

  it("is none for one group, or for facts with no group", () => {
    expect(
      detailMatrix([
        { group: "queue", name: "pending", value: "0" },
        { group: "queue", name: "failed", value: "0" },
      ]),
    ).toBeNull();
    expect(
      detailMatrix([
        ...job("a", true),
        ...job("b", true),
        { name: "version", value: "56" },
      ]),
    ).toBeNull();
    expect(detailMatrix([])).toBeNull();
  });
});

describe("roleMatrix", () => {
  it("sets out each role's facts as a row each, a role to a column", () => {
    expect(
      roleMatrix([
        { role: "api", name: "in use", value: "2" },
        { role: "worker", name: "in use", value: "5" },
        { role: "api", name: "idle", value: "8" },
      ]),
    ).toEqual({
      columns: ["api", "worker"],
      rows: [
        { label: "in use", cells: ["2", "5"] },
        { label: "idle", cells: ["8", undefined] },
      ],
    });
  });
});

describe("selectedCard", () => {
  const cards = [role("database", []), role("api", []), role("worker", [])];
  const finding = { role: "worker", component: comp("jobs") };

  it("keeps the card the address names", () => {
    expect(selectedCard(cards, "api", [finding])).toBe("api");
  });

  it("opens on the first card with a finding when the address names none, or one gone", () => {
    expect(selectedCard(cards, "", [finding])).toBe("worker");
    expect(selectedCard(cards, "scheduler", [finding])).toBe("worker");
  });

  it("opens on the first card when nothing needs attention", () => {
    expect(selectedCard(cards, "", [])).toBe("database");
  });

  it("is empty with no card", () => {
    expect(selectedCard([], "api", [])).toBe("");
  });
});
