// Install a pinned tool from its GitHub release, checked against the
// release's own SHA-256 list, and put it on PATH for the steps after.
//
// Usage: YQ_VERSION=v4.54.1 node .github/ci/install-tool.ts yq [tool …]
//        node .github/ci/install-tool.ts --cache-key yq [tool …]
//
// The version comes from the workflow's env, where a renovate comment keeps
// it current; this table says where each tool's release keeps its asset and
// its checksums, and what in the asset is the tool. A download whose SHA-256
// is not the one the release lists is refused before anything is installed.
// Nothing runs under sudo: the tool goes into RUNNER_TEMP/tools/bin.
//
// Each installed tool leaves a stamp of its version beside it, and a tool
// whose stamp names the pinned version is not downloaded again: a directory
// restored from the Actions cache (.github/actions/setup-tools) installs
// nothing, so a run does not depend on github.com/releases answering.
// --cache-key prints the key such a directory is cached under.

import { createHash } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { addPath, info } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { execute } from "./lib/exec.ts";
import { runMain } from "./lib/main.ts";
import { retry } from "./lib/retry.ts";

// RELEASES is where a GitHub release's assets download from.
const RELEASES = "https://github.com";
// TOOLS_DIR is where the tools go, under RUNNER_TEMP.
export const TOOLS_DIR = join("tools", "bin");
// EXECUTABLE is the mode an installed tool takes.
const EXECUTABLE = 0o755;
// STAMP_SUFFIX names the file beside a tool that records its version.
const STAMP_SUFFIX = ".version";
// CACHE_KEY_FLAG asks for the cache key instead of an install.
export const CACHE_KEY_FLAG = "--cache-key";
// CACHE_KEY_PREFIX starts every key, so the tools' entries are told apart
// from the Go caches in the Actions cache list.
const CACHE_KEY_PREFIX = "release-tools";

// ChecksumFormat is how a release lists its SHA-256 sums.
export const ChecksumFormat = {
  // "<sha256>  <asset>", or "<sha256> *<asset>" in binary mode.
  Sums: "sums",
  // "<asset>  <hash> <hash> …", the hashes in the order a second file names.
  Ordered: "ordered",
} as const;
export type ChecksumFormat = (typeof ChecksumFormat)[keyof typeof ChecksumFormat];

// Tool is one installable tool for a version v ("v1.2.3" as pinned).
export interface Tool {
  // versionEnv names the workflow env variable that pins it.
  versionEnv: string;
  repo: string;
  // tag is the release tag for a pinned version.
  tag: (v: string) => string;
  asset: (v: string) => string;
  checksums: (v: string) => string;
  format: ChecksumFormat;
  // order names the file listing the hash order, for an ordered list.
  order?: string;
  // member is the tool's path in an archive asset; none for a bare binary.
  member?: (v: string) => string;
  // bin is the name it is installed as, and run by.
  bin: string;
  // versionArgs print its version, to show the install worked.
  versionArgs: readonly string[];
}

const bare = (v: string) => v.replace(/^v/, "");
const asTag = (v: string) => v;
const vTag = (v: string) => `v${bare(v)}`;

export const TOOLS: Readonly<Record<string, Tool>> = {
  task: {
    versionEnv: "TASK_VERSION",
    repo: "go-task/task",
    // TASK_VERSION is pinned without its v; the tag carries one.
    tag: vTag,
    asset: () => "task_linux_amd64.tar.gz",
    checksums: () => "task_checksums.txt",
    format: ChecksumFormat.Sums,
    member: () => "task",
    bin: "task",
    versionArgs: ["--version"],
  },
  buf: {
    versionEnv: "BUF_VERSION",
    repo: "bufbuild/buf",
    // BUF_VERSION is pinned without its v; the tag carries one.
    tag: vTag,
    asset: () => "buf-Linux-x86_64",
    checksums: () => "sha256.txt",
    format: ChecksumFormat.Sums,
    bin: "buf",
    versionArgs: ["--version"],
  },
  "golangci-lint": {
    versionEnv: "GOLANGCI_LINT_VERSION",
    repo: "golangci/golangci-lint",
    tag: asTag,
    asset: (v) => `golangci-lint-${bare(v)}-linux-amd64.tar.gz`,
    checksums: (v) => `golangci-lint-${bare(v)}-checksums.txt`,
    format: ChecksumFormat.Sums,
    member: (v) => `golangci-lint-${bare(v)}-linux-amd64/golangci-lint`,
    bin: "golangci-lint",
    versionArgs: ["--version"],
  },
  yq: {
    versionEnv: "YQ_VERSION",
    repo: "mikefarah/yq",
    tag: asTag,
    asset: () => "yq_linux_amd64",
    checksums: () => "checksums",
    format: ChecksumFormat.Ordered,
    order: "checksums_hashes_order",
    bin: "yq",
    versionArgs: ["--version"],
  },
  hadolint: {
    versionEnv: "HADOLINT_VERSION",
    repo: "hadolint/hadolint",
    tag: asTag,
    asset: () => "hadolint-linux-x86_64",
    checksums: () => "checksums.sha256",
    format: ChecksumFormat.Sums,
    bin: "hadolint",
    versionArgs: ["--version"],
  },
  trivy: {
    versionEnv: "TRIVY_VERSION",
    repo: "aquasecurity/trivy",
    tag: asTag,
    asset: (v) => `trivy_${bare(v)}_Linux-64bit.tar.gz`,
    checksums: (v) => `trivy_${bare(v)}_checksums.txt`,
    format: ChecksumFormat.Sums,
    member: () => "trivy",
    bin: "trivy",
    versionArgs: ["--version"],
  },
  promtool: {
    versionEnv: "PROMETHEUS_VERSION",
    repo: "prometheus/prometheus",
    tag: asTag,
    asset: (v) => `prometheus-${bare(v)}.linux-amd64.tar.gz`,
    checksums: () => "sha256sums.txt",
    format: ChecksumFormat.Sums,
    member: (v) => `prometheus-${bare(v)}.linux-amd64/promtool`,
    bin: "promtool",
    versionArgs: ["--version"],
  },
  crane: {
    versionEnv: "CRANE_VERSION",
    repo: "google/go-containerregistry",
    tag: asTag,
    asset: () => "go-containerregistry_Linux_x86_64.tar.gz",
    checksums: () => "checksums.txt",
    format: ChecksumFormat.Sums,
    member: () => "crane",
    bin: "crane",
    versionArgs: ["version"],
  },
  step: {
    versionEnv: "STEP_VERSION",
    repo: "smallstep/cli",
    // STEP_VERSION is pinned without its v; the tag carries one.
    tag: vTag,
    asset: (v) => `step_linux_${bare(v)}_amd64.tar.gz`,
    checksums: () => "checksums.txt",
    format: ChecksumFormat.Sums,
    member: (v) => `step_${bare(v)}/bin/step`,
    bin: "step",
    versionArgs: ["version"],
  },
};

// SHA256 is the hash a release's list must name, by the name an ordered list
// gives it.
const SHA256 = "SHA-256";
const SHA256_HEX = /^[0-9a-f]{64}$/;

// sumOf is the SHA-256 a "<sha256>  <asset>" list gives asset.
export function sumOf(list: string, asset: string): string {
  for (const line of list.split("\n")) {
    const m = /^([0-9a-fA-F]{64})\s+\*?(\S+)\s*$/.exec(line.trim());
    if (m?.[1] !== undefined && m[2] === asset) return m[1].toLowerCase();
  }
  throw new Error(`no SHA-256 listed for ${asset}`);
}

// orderedSumOf is the SHA-256 an "<asset>  <hash> …" list gives asset, the
// hashes in the order the order file lists them.
export function orderedSumOf(list: string, order: string, asset: string): string {
  const index = order
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "")
    .indexOf(SHA256);
  if (index < 0) throw new Error(`the hash order names no ${SHA256}`);
  for (const line of list.split("\n")) {
    const [name, ...hashes] = line.trim().split(/\s+/);
    if (name === asset) {
      const sum = hashes[index]?.toLowerCase();
      if (sum === undefined || !SHA256_HEX.test(sum)) break;
      return sum;
    }
  }
  throw new Error(`no SHA-256 listed for ${asset}`);
}

// Fetch reads a URL's body; a test serves its own.
export type Fetch = (url: string) => Promise<Buffer>;

const download: Fetch = (url) =>
  retry(`download ${url}`, async () => {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`${url}: ${res.status} ${res.statusText}`);
    return Buffer.from(await res.arrayBuffer());
  });

// expectedSum is the SHA-256 the tool's release lists for its asset.
export async function expectedSum(tool: Tool, version: string, get: Fetch, base: string = RELEASES): Promise<string> {
  const at = (file: string) => `${base}/${tool.repo}/releases/download/${tool.tag(version)}/${file}`;
  const asset = tool.asset(version);
  const list = (await get(at(tool.checksums(version)))).toString("utf8");
  if (tool.format === ChecksumFormat.Ordered) {
    if (tool.order === undefined) throw new Error(`${tool.bin}: an ordered list needs its order file`);
    return orderedSumOf(list, (await get(at(tool.order))).toString("utf8"), asset);
  }
  return sumOf(list, asset);
}

// stampOf is the file beside tool in dir that records its version.
const stampOf = (tool: Tool, dir: string) => join(dir, tool.bin + STAMP_SUFFIX);

// installed reports whether dir holds tool at version: the tool, and a stamp
// naming that version beside it.
export function installed(tool: Tool, version: string, dir: string): boolean {
  const stamp = stampOf(tool, dir);
  return existsSync(join(dir, tool.bin)) && existsSync(stamp) && readFileSync(stamp, "utf8") === version;
}

// cacheKey is the key a directory holding tools is cached under: it names
// each tool at its version, and the installer, so a new pin or a change to
// how a tool is installed misses the cache rather than restoring the old.
export function cacheKey(tools: readonly { name: string; version: string }[], installer: string): string {
  const h = createHash("sha256").update(installer);
  for (const { name, version } of [...tools].sort((a, b) => a.name.localeCompare(b.name))) {
    h.update(`\n${name}@${version}`);
  }
  return `${CACHE_KEY_PREFIX}-${h.digest("hex")}`;
}

// install downloads the tool at version, refuses it unless its SHA-256 is
// the listed one, and writes it into dir as tool.bin with its stamp; it
// returns its path. A tool dir already holds at version is not downloaded.
export async function install(tool: Tool, version: string, dir: string, get: Fetch = download, base: string = RELEASES): Promise<string> {
  const target = join(dir, tool.bin);
  if (installed(tool, version, dir)) {
    info(`${tool.bin} ${version}: already installed, not downloaded`);
    return target;
  }
  const asset = tool.asset(version);
  const want = await expectedSum(tool, version, get, base);
  const body = await get(`${base}/${tool.repo}/releases/download/${tool.tag(version)}/${asset}`);
  const got = createHash("sha256").update(body).digest("hex");
  if (got !== want) {
    throw new Error(`${asset}: SHA-256 ${got}, the release lists ${want}; refusing to install it`);
  }
  mkdirSync(dir, { recursive: true });
  if (tool.member === undefined) {
    writeFileSync(target, body);
  } else {
    const work = mkdtempSync(join(dir, `.${tool.bin}-`));
    try {
      const archive = join(work, asset);
      writeFileSync(archive, body);
      const member = tool.member(version);
      await execute("tar", ["-xzf", archive, "-C", work, member]);
      renameSync(join(work, member), target);
    } finally {
      rmSync(work, { recursive: true, force: true });
    }
  }
  chmodSync(target, EXECUTABLE);
  writeFileSync(stampOf(tool, dir), version);
  return target;
}

// toolsOf is each named tool with its pinned version.
function toolsOf(names: readonly string[]): { name: string; tool: Tool; version: string }[] {
  const usage = `usage: install-tool.ts [${CACHE_KEY_FLAG}] ${Object.keys(TOOLS).join("|")} …`;
  if (names.length === 0) throw new Error(usage);
  return names.map((name) => {
    const tool = TOOLS[name];
    if (tool === undefined) throw new Error(`${name}: no such tool; ${usage}`);
    return { name, tool, version: requireEnv(tool.versionEnv) };
  });
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  if (args[0] === CACHE_KEY_FLAG) {
    process.stdout.write(`${cacheKey(toolsOf(args.slice(1)), readFileSync(new URL(import.meta.url), "utf8"))}\n`);
    return;
  }
  const dir = join(optionalEnv("RUNNER_TEMP") ?? requireEnv("TMPDIR"), TOOLS_DIR);
  for (const { tool, version } of toolsOf(args)) {
    const path = await install(tool, version, dir);
    info((await execute(path, tool.versionArgs)).trim());
  }
  addPath(dir);
}

if (import.meta.main) {
  await runMain(main);
}
