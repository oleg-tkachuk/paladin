import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { MAX_SEGMENT_BYTES } from "@/lib/objects/objectKey";
import {
  DOT_SEGMENT_REASON,
  DUPLICATE_GRANT,
  MAX_COLLECTION_LENGTH,
  TENANT_UNKNOWN,
  collectionSegment,
  firstObjectGrantProblem,
  grantKeyProblem,
  isCollectionName,
  objectGrantProblem,
  objectGrantProblems,
  objectResource,
} from "./objectResource";

// The table the server and both SDKs are held to as well.
const SHARED = resolve(
  __dirname,
  "../../../../sdk/testdata/object_resources.json",
);
const TENANT_PLACEHOLDER = "{tenant}";

type Row = { collection: string; key: string; uri: string };
const table = JSON.parse(readFileSync(SHARED, "utf8")) as {
  tenant: string;
  resources: Row[];
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

describe("collection segments", () => {
  it.each([
    ["docs", "docs"],
    ["invoices/2026/q1", "invoices.2026.q1"],
  ])("writes %s as %s", (collection, segment) => {
    expect(collectionSegment(collection)).toBe(segment);
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
