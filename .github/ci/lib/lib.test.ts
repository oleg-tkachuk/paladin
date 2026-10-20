import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, test } from "node:test";

import { ANNOTATIONS_ENV, ANNOTATIONS_OFF, Level, annotation, cell, escapeData, setOutput, table } from "./actions.ts";
import { optionalEnv, requireEnv } from "./env.ts";
import { lines } from "./git.ts";

// VARIABLE is the environment variable these tests set and clear.
const VARIABLE = "PALADIN_CI_TEST_VARIABLE";

describe("setOutput", () => {
  let dir: string;
  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "paladin-ci-"));
    process.env.GITHUB_OUTPUT = join(dir, "output");
  });
  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
    delete process.env.GITHUB_OUTPUT;
  });

  test("appends name=value", () => {
    setOutput("relevant", "true");
    setOutput("tag", "v1.2.3");
    assert.equal(readFileSync(join(dir, "output"), "utf8"), "relevant=true\ntag=v1.2.3\n");
  });
  test("refuses a value that spans lines", () => {
    assert.throws(() => setOutput("tag", "v1\nrelevant=false"), /spans lines/);
  });
  test("refuses without the output file", () => {
    delete process.env.GITHUB_OUTPUT;
    assert.throws(() => setOutput("tag", "v1"), /GITHUB_OUTPUT is not set/);
  });
});

test("escapeData keeps a message one command", () => {
  assert.equal(escapeData("50%\r\n::error::x"), "50%25%0D%0A::error::x");
});

describe("env", () => {
  afterEach(() => delete process.env[VARIABLE]);
  test("requireEnv names what is missing", () => assert.throws(() => requireEnv(VARIABLE), new RegExp(`${VARIABLE} is not set`)));
  test("an empty value is unset", () => {
    process.env[VARIABLE] = "";
    assert.equal(optionalEnv(VARIABLE), undefined);
    assert.throws(() => requireEnv(VARIABLE));
  });
  test("a value is read", () => {
    process.env[VARIABLE] = "x";
    assert.equal(requireEnv(VARIABLE), "x");
    assert.equal(optionalEnv(VARIABLE), "x");
  });
});

test("lines drops empty lines", () => assert.deepEqual(lines("a\n\nb\n"), ["a", "b"]));

describe("annotation", () => {
  const saved = process.env[ANNOTATIONS_ENV];
  afterEach(() => {
    if (saved === undefined) delete process.env[ANNOTATIONS_ENV];
    else process.env[ANNOTATIONS_ENV] = saved;
  });
  test("a workflow command, escaped", () => {
    delete process.env[ANNOTATIONS_ENV];
    assert.equal(annotation(Level.Error, "a\nb"), "::error::a%0Ab");
  });
  test("a plain line while annotations are off", () => {
    process.env[ANNOTATIONS_ENV] = ANNOTATIONS_OFF;
    assert.equal(annotation(Level.Warning, "x"), "warning: x");
  });
});

// The Taskfile turns annotations off for these tests by the same name and
// value actions.ts reads.
test("verify:ci runs the tests with annotations off", async () => {
  const { readFileSync } = await import("node:fs");
  const { parse } = await import("yaml");
  const taskfile = parse(readFileSync(new URL("../../../Taskfile.dev.yaml", import.meta.url), "utf8")) as {
    tasks: Record<string, { env?: Record<string, string> }>;
  };
  assert.equal(taskfile.tasks["verify:ci"]?.env?.[ANNOTATIONS_ENV], ANNOTATIONS_OFF);
});

test("a table cell holds no line break and no column separator", () => {
  assert.equal(cell("a|b\nc"), "a\\|b c");
  assert.equal(table(["A", "B"], [["1", "x|y"]]), "| A | B |\n| --- | --- |\n| 1 | x\\|y |\n");
});

describe("globs", async () => {
  const { globToRegExp, matchesAny } = await import("./glob.ts");
  test("* stays within a segment", () => {
    assert.ok(globToRegExp("Taskfile*.yaml").test("Taskfile.dev.yaml"));
    assert.ok(!globToRegExp("*.md").test("docs/a.md"));
  });
  test("**/ spans directories, the root included", () => {
    assert.ok(globToRegExp("**/*.md").test("README.md"));
    assert.ok(globToRegExp("**/*.md").test("backend/docs/a.md"));
    assert.ok(!globToRegExp("**/*.md").test("README.mdx"));
  });
  test("a trailing ** takes everything under a directory", () => {
    assert.ok(globToRegExp("docs/**").test("docs/a/b.svg"));
    assert.ok(!globToRegExp("docs/**").test("backend/docs/a.go"));
  });
  test("other characters are literal", () => {
    assert.ok(!globToRegExp(".gitignore").test("xgitignore"));
    assert.ok(matchesAny("LICENSE", ["NOTICE", "LICENSE"]));
  });
});

describe("retry", async () => {
  const { retry, RETRY_ATTEMPTS } = await import("./retry.ts");
  const flaky = (failures: number) => {
    let calls = 0;
    const fn = async () => {
      calls++;
      if (calls <= failures) throw new Error(`failure ${calls}`);
      return calls;
    };
    return { fn, calls: () => calls };
  };
  test("a call that succeeds runs once", async () => {
    const f = flaky(0);
    assert.equal(await retry("x", f.fn, { delayMs: 0 }), 1);
    assert.equal(f.calls(), 1);
  });
  test("one that succeeds on the last attempt is a success", async () => {
    const f = flaky(RETRY_ATTEMPTS - 1);
    assert.equal(await retry("x", f.fn, { delayMs: 0 }), RETRY_ATTEMPTS);
  });
  test("one failing every attempt fails with its own last error", async () => {
    const f = flaky(99);
    await assert.rejects(retry("x", f.fn, { attempts: 3, delayMs: 0 }), /failure 3/);
    assert.equal(f.calls(), 3);
  });
  test("each wait doubles the one before", async () => {
    const waits: number[] = [];
    await assert.rejects(retry("x", flaky(99).fn, { attempts: 4, delayMs: 5, sleep: async (ms) => void waits.push(ms) }));
    assert.deepEqual(waits, [5, 10, 20]);
  });
  test("one attempt is one call", async () => {
    const f = flaky(99);
    await assert.rejects(retry("x", f.fn, { attempts: 1, delayMs: 0 }));
    assert.equal(f.calls(), 1);
  });
});
