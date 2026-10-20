// Install a pinned tool from its GitHub release, checked against the
// release's own SHA-256 list, and put it on PATH for the steps after.
//
// Usage: YQ_VERSION=v4.54.1 node .github/ci/install-tool.ts yq
//
// The version comes from the workflow's env, where a renovate comment keeps
// it current; this table says where each tool's release keeps its asset and
// its checksums, and what in the asset is the tool. A download whose SHA-256
// is not the one the release lists is refused before anything is installed.
// Nothing runs under sudo: the tool goes into RUNNER_TEMP/tools/bin.

import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from "node:fs";
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

// install downloads the tool at version, refuses it unless its SHA-256 is
// the listed one, and writes it into dir as tool.bin; it returns its path.
export async function install(tool: Tool, version: string, dir: string, get: Fetch = download, base: string = RELEASES): Promise<string> {
  const asset = tool.asset(version);
  const want = await expectedSum(tool, version, get, base);
  const body = await get(`${base}/${tool.repo}/releases/download/${tool.tag(version)}/${asset}`);
  const got = createHash("sha256").update(body).digest("hex");
  if (got !== want) {
    throw new Error(`${asset}: SHA-256 ${got}, the release lists ${want}; refusing to install it`);
  }
  mkdirSync(dir, { recursive: true });
  const target = join(dir, tool.bin);
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
  return target;
}

async function main(): Promise<void> {
  const [name] = process.argv.slice(2);
  const tool = name === undefined ? undefined : TOOLS[name];
  if (tool === undefined) {
    throw new Error(`usage: install-tool.ts ${Object.keys(TOOLS).join("|")}`);
  }
  const version = requireEnv(tool.versionEnv);
  const dir = join(optionalEnv("RUNNER_TEMP") ?? requireEnv("TMPDIR"), TOOLS_DIR);
  const path = await install(tool, version, dir);
  addPath(dir);
  info((await execute(path, tool.versionArgs)).trim());
}

if (import.meta.main) {
  await runMain(main);
}
