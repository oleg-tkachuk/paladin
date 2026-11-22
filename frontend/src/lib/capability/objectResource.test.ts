import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { MAX_SEGMENT_BYTES } from "@/lib/objects/objectKey";
import {
  DOT_SEGMENT_REASON,
  DUPLICATE_GRANT,
  GRANT_REACH,
  MAX_COLLECTION_LENGTH,
  OBJECT_RESOURCE_SCHEME,
  TENANT_UNKNOWN,
  collectionSegment,
  describeObjectGrant,
  firstObjectGrantProblem,
  grantKeyProblem,
  isCollectionName,
  objectGrantProblem,
  objectGrantProblems,
  objectResource,
  parseCollectionSegment,
  parseObjectResource,
} from "./objectResource";

// The table the server and both SDKs are held to as well.
const SHARED = resolve(
  __dirname,
  "../../../../sdk/testdata/object_resources.json",
);
const TENANT_PLACEHOLDER = "{tenant}";
const UPPER_TENANT_PLACEHOLDER = "{TENANT}";
const OTHER_TENANT_PLACEHOLDER = "{other_tenant}";

type Row = { collection: string; key: string; uri: string };
type ReachRow = { name: string; prefix: string; reach: string };
const table = JSON.parse(readFileSync(SHARED, "utf8")) as {
  tenant: string;
  other_tenant: string;
  resources: Row[];
  reach: ReachRow[];
};
const TENANT = table.tenant;
const uriOf = (row: Row) => row.uri.replace(TENANT_PLACEHOLDER, TENANT);

describe("objectResource", () => {
  it.each(table.resources)(
    "builds the shared table's resource for $collection + $key",
    (row) => {
      expect(objectResource(TENANT, row.collection, row.key)).toBe(uriOf(row));
    },
  );

  it("writes the tenant id in the server's lower case", () => {
    expect(objectResource(TENANT.toUpperCase(), "docs", "a.pdf")).toBe(
      objectResource(TENANT, "docs", "a.pdf"),
    );
  });

  // The aliasing the segment encoding exists to end.
  it("gives a nested collection and a key under its parent different resources", () => {
    expect(objectResource(TENANT, "a/b", "c")).not.toBe(
      objectResource(TENANT, "a", "b/c"),
    );
  });
});

describe("parseObjectResource", () => {
  it.each(table.resources)("reads back $uri", (row) => {
    expect(parseObjectResource(uriOf(row))).toEqual({
      tenant: TENANT,
      collection: row.collection,
      key: row.key,
    });
  });

  it.each([
    ["no scheme", `${TENANT}/docs/a.pdf`],
    ["another scheme", `kv://${TENANT}/docs/a.pdf`],
    ["no collection", `${OBJECT_RESOURCE_SCHEME}${TENANT}`],
    ["no key part", `${OBJECT_RESOURCE_SCHEME}${TENANT}/docs`],
    ["a tenant that is not an id", `${OBJECT_RESOURCE_SCHEME}acme/docs/a.pdf`],
    ["an empty collection", `${OBJECT_RESOURCE_SCHEME}${TENANT}//a.pdf`],
    ["an upper-case collection", `${OBJECT_RESOURCE_SCHEME}${TENANT}/Docs/a`],
    ["an empty nested name", `${OBJECT_RESOURCE_SCHEME}${TENANT}/a..b/c`],
    ["the old path-like form", "objects/contracts/2026/"],
  ])("refuses %s", (_, uri) => {
    expect(parseObjectResource(uri)).toBeNull();
  });
});

describe("describeObjectGrant", () => {
  // Each stored prefix is worded by what the server's match reaches with it;
  // the server's test holds limes.MatchResource to the same rows.
  it.each(table.reach)("words $name as $reach", (row) => {
    const prefix = row.prefix
      .replaceAll(TENANT_PLACEHOLDER, TENANT)
      .replaceAll(UPPER_TENANT_PLACEHOLDER, TENANT.toUpperCase())
      .replaceAll(OTHER_TENANT_PLACEHOLDER, table.other_tenant);
    expect(describeObjectGrant(prefix, TENANT).reach).toBe(row.reach);
  });

  it("holds every reach the table names", () => {
    expect(new Set(table.reach.map((r) => r.reach))).toEqual(
      new Set(Object.values(GRANT_REACH)),
    );
  });

  it.each([
    ["", "docs", "The whole collection docs and every object in it."],
    ["reports/", "docs", "The objects under reports/ in docs."],
    [
      "reports",
      "docs",
      "The object reports in docs, and the objects under reports/.",
    ],
    [
      "r.pdf",
      "invoices/2026/q1",
      "The object r.pdf in invoices/2026/q1, and the objects under r.pdf/.",
    ],
  ])("reads key %j in %s", (key, collection, words) => {
    expect(
      describeObjectGrant(objectResource(TENANT, collection, key), TENANT).text,
    ).toBe(words);
  });

  it.each([
    [
      `${OBJECT_RESOURCE_SCHEME}${TENANT}`,
      "Every object in every collection of this tenant.",
    ],
    [
      `${OBJECT_RESOURCE_SCHEME}${TENANT}/docs`,
      "The whole collection docs and every object in it.",
    ],
    [
      OBJECT_RESOURCE_SCHEME,
      "Every object of every tenant, as far as this prefix goes: it restricts nothing.",
    ],
    [
      `${OBJECT_RESOURCE_SCHEME}${table.other_tenant}/docs/`,
      `Another tenant (${table.other_tenant}): nothing in this tenant.`,
    ],
    [
      `${OBJECT_RESOURCE_SCHEME}${TENANT.toUpperCase()}/docs/`,
      "The tenant id is not in lower case, as the server writes it: it reaches nothing.",
    ],
    [
      `${OBJECT_RESOURCE_SCHEME}${TENANT}/Docs/`,
      'No collection is written "Docs": it reaches nothing.',
    ],
    [
      `${OBJECT_RESOURCE_SCHEME}${TENANT}/Docs`,
      'No collection is written "Docs": it reaches nothing.',
    ],
    [
      `${OBJECT_RESOURCE_SCHEME}${TENANT}/docs/a/../b`,
      'The key part is refused (it holds a ".." segment): it reaches nothing.',
    ],
    ["obj", "Not an object resource: the console cannot say what it reaches."],
    [
      `${OBJECT_RESOURCE_SCHEME}${TENANT}/docs/%2e%2e/`,
      `The key part is refused (${DOT_SEGMENT_REASON}): it reaches nothing.`,
    ],
    [
      "objects/contracts/2026/",
      "Not an object resource: the console cannot say what it reaches.",
    ],
  ])("words %j", (prefix, words) => {
    expect(describeObjectGrant(prefix, TENANT).text).toBe(words);
  });

  // An unknown tenant cannot make the panel call the tenant's own resource
  // another tenant's.
  it("reads every resource as its own while the tenant is unknown", () => {
    expect(
      describeObjectGrant(objectResource(TENANT, "docs", ""), "").reach,
    ).toBe(GRANT_REACH.collection);
  });

  it("compares the tenant as the server writes it", () => {
    expect(
      describeObjectGrant(
        objectResource(TENANT, "docs", ""),
        TENANT.toUpperCase(),
      ).reach,
    ).toBe(GRANT_REACH.collection);
  });
});

describe("collection segments", () => {
  it.each([
    ["docs", "docs"],
    ["invoices/2026/q1", "invoices.2026.q1"],
  ])("writes %s as %s and reads it back", (collection, segment) => {
    expect(collectionSegment(collection)).toBe(segment);
    expect(parseCollectionSegment(segment)).toBe(collection);
  });

  it("refuses a segment holding the separator", () => {
    expect(parseCollectionSegment("a/b")).toBeNull();
  });

  it.each([
    ["a", true],
    ["a-b/c9", true],
    ["-a", false],
    ["a-", false],
    ["a_b", false],
    ["a//b", false],
    ["", false],
    ["a".repeat(64), false],
  ])("holds %s to the collection grammar: %s", (name, ok) => {
    expect(isCollectionName(name)).toBe(ok);
  });

  // 64 names of three letters and their separators: exactly the limit.
  it("admits a name at the length limit and refuses one past it", () => {
    const atLimit = Array.from({ length: 64 }, () => "abc").join("/");
    expect(atLimit.length).toBe(MAX_COLLECTION_LENGTH);
    expect(isCollectionName(atLimit)).toBe(true);
    expect(isCollectionName(`${atLimit}d`)).toBe(false);
  });
});

describe("grantKeyProblem", () => {
  it.each(["", "a.pdf", "reports/", "2026/q3/report.pdf", "2026/q3/"])(
    "admits %j",
    (key) => {
      expect(grantKeyProblem(key)).toBeNull();
    },
  );

  it.each([
    ["/", 'it starts with "/"'],
    ["/reports/", 'it starts with "/"'],
    ["reports//", 'it ends with "/"'],
    ["a//b", "it holds an empty segment"],
    ["a/../b", 'it holds a ".." segment'],
    ["a/..", 'it holds a ".." segment'],
    ["./a", 'it holds a "." segment'],
    ["a\u0000b", "it holds a control character"],
    ["%2e%2e/x", DOT_SEGMENT_REASON],
    ["..;x/", DOT_SEGMENT_REASON],
    ["a/%252e%252e/", DOT_SEGMENT_REASON],
  ])("refuses %j: %s", (key, reason) => {
    expect(grantKeyProblem(key)).toBe(reason);
  });

  it("refuses a segment over the store's limit", () => {
    expect(grantKeyProblem("x".repeat(MAX_SEGMENT_BYTES + 1))).toMatch(
      /over the limit/,
    );
  });
});

describe("objectGrantProblems", () => {
  it("asks for a collection first", () => {
    expect(objectGrantProblem({ collection: "", key: "reports/" })).toBe(
      "Choose a collection.",
    );
  });

  it("names the refused key prefix", () => {
    expect(objectGrantProblem({ collection: "docs", key: "a//b" })).toBe(
      "The key prefix is refused: it holds an empty segment.",
    );
  });

  it("refuses a row repeating an earlier one, and only the later", () => {
    const drafts = [
      { collection: "docs", key: "reports/" },
      { collection: "docs", key: "" },
      { collection: "docs", key: "reports/" },
    ];
    expect(objectGrantProblems(TENANT, drafts)).toEqual([
      null,
      null,
      DUPLICATE_GRANT,
    ]);
    expect(firstObjectGrantProblem(TENANT, drafts)).toEqual({
      row: 3,
      reason: DUPLICATE_GRANT,
    });
  });

  it("refuses every row while the tenant has no id", () => {
    expect(
      objectGrantProblems("acme", [{ collection: "docs", key: "" }]),
    ).toEqual([TENANT_UNKNOWN]);
  });

  it("has nothing to refuse in no rows", () => {
    expect(firstObjectGrantProblem("", [])).toBeNull();
  });
});
