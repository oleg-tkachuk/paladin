// A release-cutting commit whose proto change is a comment alone.
//
// Usage: BASE=<sha> HEAD=<sha> node proto-comments.ts
//
// A proto's comments are documentation (docs/releasing.md): a change to them
// alone is docs(proto), committed apart from the change it describes. The
// analyzer reads a commit's type and paths, so a feat that changes the server
// and rewrites a field's comment under proto/ also cuts an SDK release whose
// code is unchanged — sdk/go/v0.98.0 and v0.99.0 were two, each a server
// feature that updated a comment. This check refuses such a commit in its
// pull request instead.
//
// A commit is refused when the release analyzer would cut a release for it on
// any stream, it changes a file under proto/, and the contract compiled
// without its comments (buf build --exclude-source-info) is the same before
// and after it.

import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { analyzeWith, filesOf } from "../release/paths-analyzer.mjs";
import { error, info } from "./lib/actions.ts";
import { requireEnv } from "./lib/env.ts";
import { execute } from "./lib/exec.ts";
import { git, lines } from "./lib/git.ts";
import { runMain } from "./lib/main.ts";
import { streams } from "./lib/release-config.ts";

// PROTO_ROOT is where the contract lives, the buf module's root.
export const PROTO_ROOT = "proto/";

const SILENT = { log() {}, error() {}, warn() {}, success() {} };

// releases reports whether the release analyzer cuts a release on any stream
// for a commit with message that changed files.
export async function releases(message: string, files: readonly string[]): Promise<boolean> {
  const require = createRequire(import.meta.url);
  const { analyzerFor } = require("../release/release.config.cjs") as { analyzerFor: (name: string) => Record<string, unknown> };
  for (const name of Object.keys(streams())) {
    const release = await analyzeWith(analyzerFor(name), { commits: [{ hash: "0", message }], logger: SILENT, cwd: process.cwd(), options: {} }, () => [...files]);
    if (release) return true;
  }
  return false;
}

// descriptors is the contract at commit, compiled without its comments.
export async function descriptors(commit: string, root: string): Promise<Buffer> {
  const dir = mkdtempSync(join(tmpdir(), "paladin-proto-"));
  try {
    const archive = join(dir, "proto.tar");
    await git(["archive", "--output", archive, commit, PROTO_ROOT], root);
    await execute("tar", ["-xf", archive, "-C", dir]);
    const image = join(dir, "image.binpb");
    await execute("buf", ["build", join(dir, PROTO_ROOT), "--exclude-source-info", "-o", image]);
    return readFileSync(image);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

// Refusal is one commit this check refuses.
export interface Refusal {
  commit: string;
  subject: string;
}

// commentOnly lists the commits between base and head that cut a release for
// a proto change that is a comment alone.
export async function commentOnly(base: string, head: string, root: string): Promise<Refusal[]> {
  const refused: Refusal[] = [];
  for (const commit of lines(await git(["rev-list", "--no-merges", "--reverse", `${base}..${head}`], root))) {
    const files = filesOf(commit, root);
    if (!files.some((f) => f.startsWith(PROTO_ROOT))) continue;
    const message = await git(["log", "-1", "--format=%B", commit], root);
    if (!(await releases(message, files))) continue;
    if (!(await descriptors(`${commit}^`, root)).equals(await descriptors(commit, root))) continue;
    refused.push({ commit, subject: lines(message)[0] ?? "" });
  }
  return refused;
}

async function main(): Promise<void> {
  const root = (await git(["rev-parse", "--show-toplevel"])).trim();
  const refused = await commentOnly(requireEnv("BASE"), requireEnv("HEAD"), root);
  if (refused.length === 0) {
    info("no release-cutting commit changes only a proto comment");
    return;
  }
  for (const r of refused) {
    error(`${r.commit.slice(0, 12)} "${r.subject}" changes proto/ only in its comments, and would cut a release: commit the comments apart as docs(proto) (docs/releasing.md)`);
  }
  throw new Error(`${refused.length} commit(s) would release a proto comment`);
}

if (import.meta.main) {
  await runMain(main);
}
