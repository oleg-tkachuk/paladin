import { describe, expect, it } from "vitest";

import { splitDatabase, type Component, type Snapshot } from "./health";

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
});
