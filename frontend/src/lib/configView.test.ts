import { describe, expect, it } from "vitest";

import {
  REDACTED,
  filterConfigSections,
  parseConfigSections,
  redactedCount,
} from "./configView";

const YAML = `app:
  name: paladin-core
  env: prod
logger:
  level: info
  sampling:
    enabled: false
    initial: 100
datastores:
  postgres:
    dsn: postgres://paladin_app@pg:5432/paladin
    password: '${REDACTED}'
    password_secret: null
middleware:
  trusted_proxies:
    - 10.0.0.0/8
    - fc00::/7
  cors: {}
`;

describe("parseConfigSections", () => {
  const sections = parseConfigSections(YAML);

  it("keeps the server's top-level order", () => {
    expect(sections.map((s) => s.key)).toEqual([
      "app",
      "logger",
      "datastores",
      "middleware",
    ]);
  });

  it("walks maps into dotted paths and types each value", () => {
    const logger = sections.find((s) => s.key === "logger")!;
    expect(logger.entries).toEqual([
      { path: "level", value: { kind: "text", text: "info" } },
      { path: "sampling.enabled", value: { kind: "bool", value: false } },
      { path: "sampling.initial", value: { kind: "number", text: "100" } },
    ]);
  });

  it("tells a redacted secret from an unset one", () => {
    const pg = sections.find((s) => s.key === "datastores")!.entries;
    expect(pg.find((e) => e.path === "postgres.password")?.value).toEqual({
      kind: "redacted",
    });
    expect(
      pg.find((e) => e.path === "postgres.password_secret")?.value,
    ).toEqual({
      kind: "empty",
    });
  });

  it("keeps a list as one entry, and an empty map as empty", () => {
    const mw = sections.find((s) => s.key === "middleware")!.entries;
    expect(mw).toEqual([
      {
        path: "trusted_proxies",
        value: { kind: "list", items: ["10.0.0.0/8", "fc00::/7"] },
      },
      { path: "cors", value: { kind: "empty" } },
    ]);
  });

  it("gives each section its own YAML to copy", () => {
    const app = sections.find((s) => s.key === "app")!;
    expect(app.yaml).toBe("app:\n  name: paladin-core\n  env: prod\n");
  });

  it("reads nothing out of an empty document, or one that is not a map", () => {
    expect(parseConfigSections("")).toEqual([]);
    expect(parseConfigSections("- a\n- b\n")).toEqual([]);
  });

  it("throws on a document that is not YAML", () => {
    expect(() => parseConfigSections("a: [unclosed")).toThrow();
  });
});

describe("filterConfigSections", () => {
  const sections = parseConfigSections(YAML);
  const keys = (q: string) =>
    filterConfigSections(sections, q).map((s) => [
      s.key,
      s.entries.map((e) => e.path),
    ]);

  it.each([
    ["", 4],
    ["  ", 4],
  ])("%j keeps every section", (q, n) => {
    expect(filterConfigSections(sections, q)).toHaveLength(n);
  });

  it("matches a section name and keeps all of it", () => {
    expect(keys("LOGGER")).toEqual([
      ["logger", ["level", "sampling.enabled", "sampling.initial"]],
    ]);
  });

  it("matches a path, below the section name", () => {
    expect(keys("sampling.init")).toEqual([["logger", ["sampling.initial"]]]);
    expect(keys("logger.level")).toEqual([["logger", ["level"]]]);
  });

  it("matches a value, a list item included", () => {
    expect(keys("paladin_app")).toEqual([["datastores", ["postgres.dsn"]]]);
    expect(keys("fc00")).toEqual([["middleware", ["trusted_proxies"]]]);
  });

  it("leaves out what nothing matches", () => {
    expect(keys("kafka")).toEqual([]);
  });
});

describe("redactedCount", () => {
  it("counts the secrets the server hid", () => {
    expect(redactedCount(parseConfigSections(YAML))).toBe(1);
  });
});
