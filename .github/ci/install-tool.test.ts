import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { parse } from "yaml";

import { execute } from "./lib/exec.ts";
import { ChecksumFormat, TOOLS, install, orderedSumOf, sumOf, type Fetch, type Tool } from "./install-tool.ts";

const SUM = "a".repeat(64);

describe("sumOf", () => {
  test("a text-mode line", () => assert.equal(sumOf(`${"b".repeat(64)}  other\n${SUM}  tool.tgz\n`, "tool.tgz"), SUM));
  test("a binary-mode line, as hadolint lists it", () => assert.equal(sumOf(`${SUM.toUpperCase()} *tool\n`, "tool"), SUM));
  test("an asset it does not list", () => assert.throws(() => sumOf(`${SUM}  tool.tgz.sig\n`, "tool.tgz"), /no SHA-256 listed for tool.tgz/));
});

describe("orderedSumOf", () => {
  const order = "CRC32\nMD5\nSHA-256\nSHA-512\n";
  test("the hash at SHA-256's place in the order", () => {
    assert.equal(orderedSumOf(`yq_linux_arm64  1 2 ${"c".repeat(64)} 4\nyq_linux_amd64  1 2 ${SUM} 4\n`, order, "yq_linux_amd64"), SUM);
  });
  test("an order that names no SHA-256", () => assert.throws(() => orderedSumOf(`yq  ${SUM}\n`, "MD5\n", "yq"), /names no SHA-256/));
  test("a hash at that place that is not one", () => assert.throws(() => orderedSumOf("yq  1 2 short\n", order, "yq"), /no SHA-256 listed/));
});

// Every pin the table reads is one a workflow sets, where renovate keeps it.
test("each tool's version is pinned in a workflow's env", () => {
  const env = ["ci.yaml", "release.yaml"].flatMap((w) =>
    Object.keys((parse(readFileSync(new URL(`../workflows/${w}`, import.meta.url), "utf8")) as { env: Record<string, string> }).env),
  );
  for (const [name, tool] of Object.entries(TOOLS)) assert.ok(env.includes(tool.versionEnv), `${name}: ${tool.versionEnv}`);
});

test("each tool's release names its assets as published", () => {
  const at = (t: Tool, v: string) => [t.tag(v), t.asset(v), t.checksums(v), t.member?.(v)];
  assert.deepEqual(at(TOOLS.trivy!, "v0.75.0"), ["v0.75.0", "trivy_0.75.0_Linux-64bit.tar.gz", "trivy_0.75.0_checksums.txt", "trivy"]);
  assert.deepEqual(at(TOOLS.promtool!, "v3.15.0"), ["v3.15.0", "prometheus-3.15.0.linux-amd64.tar.gz", "sha256sums.txt", "prometheus-3.15.0.linux-amd64/promtool"]);
  assert.deepEqual(at(TOOLS.step!, "0.31.0"), ["v0.31.0", "step_linux_0.31.0_amd64.tar.gz", "checksums.txt", "step_0.31.0/bin/step"]);
  assert.equal(TOOLS.yq!.format, ChecksumFormat.Ordered);
});

describe("install", () => {
  const BASE = "https://example.invalid";
  const sha = (b: Buffer) => createHash("sha256").update(b).digest("hex");
  // served answers each release URL from files, and fails any other.
  const served = (files: Record<string, Buffer>): Fetch => async (url) => {
    const name = url.split("/").at(-1) ?? "";
    const file = files[name];
    if (file === undefined) throw new Error(`404 ${url}`);
    return file;
  };
  const binaryTool: Tool = { versionEnv: "X", repo: "o/t", tag: (v) => v, asset: () => "tool-linux", checksums: () => "sums.txt", format: ChecksumFormat.Sums, bin: "tool", versionArgs: [] };

  test("a bare binary, written executable", async () => {
    const dir = mkdtempSync(join(tmpdir(), "paladin-ci-"));
    try {
      const body = Buffer.from("#!/bin/sh\necho tool 1.0\n");
      const path = await install(binaryTool, "v1", dir, served({ "tool-linux": body, "sums.txt": Buffer.from(`${sha(body)}  tool-linux\n`) }), BASE);
      assert.equal(readFileSync(path, "utf8"), body.toString());
      assert.ok((statSync(path).mode & 0o111) !== 0, "executable");
      assert.equal((await execute(path, [])).trim(), "tool 1.0");
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  test("the one member of an archive", async () => {
    const dir = mkdtempSync(join(tmpdir(), "paladin-ci-"));
    try {
      const src = join(dir, "src");
      mkdirSync(join(src, "pkg-1"), { recursive: true });
      writeFileSync(join(src, "pkg-1", "tool"), "#!/bin/sh\necho archived\n");
      writeFileSync(join(src, "pkg-1", "README"), "not the tool");
      await execute("tar", ["-czf", join(dir, "pkg.tgz"), "-C", src, "pkg-1"]);
      const body = readFileSync(join(dir, "pkg.tgz"));
      const tool: Tool = { ...binaryTool, asset: () => "pkg.tgz", member: () => "pkg-1/tool" };
      const out = join(dir, "bin");
      const path = await install(tool, "v1", out, served({ "pkg.tgz": body, "sums.txt": Buffer.from(`${sha(body)}  pkg.tgz\n`) }), BASE);
      assert.equal((await execute(path, [])).trim(), "archived");
      assert.ok(!existsSync(join(out, "README")), "only the tool is installed");
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  test("a download whose SHA-256 is not the listed one is refused, and nothing installed", async () => {
    const dir = mkdtempSync(join(tmpdir(), "paladin-ci-"));
    try {
      const get = served({ "tool-linux": Buffer.from("tampered"), "sums.txt": Buffer.from(`${SUM}  tool-linux\n`) });
      await assert.rejects(install(binaryTool, "v1", dir, get, BASE), /refusing to install it/);
      assert.ok(!existsSync(join(dir, "tool")));
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
