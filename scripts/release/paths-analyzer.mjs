// paths-analyzer.mjs — a semantic-release analyzeCommits plugin that decides a
// release from the commits touching one part of the repository.
//
// The bump itself is @semantic-release/commit-analyzer's, with the options it
// is given. This only chooses which commits it reads, by the files each one
// changed: `include` keeps a commit that touches any of those prefixes,
// `exclude` drops one whose every file is under them. So the SDK releases for
// a change to proto/ whatever its commit's scope, and the product does not
// release for a commit that only touched the SDK.
//
// `modules` re-reads the commits whose every file is under `modules.paths`
// with `modules.releaseRules` added: a break in the API of a module the
// product compiles in — the Go SDK, the capability module — is that module's
// break, not the product's, and must not cut a product major.

import { execFileSync } from "node:child_process";
import { analyzeCommits as analyze } from "@semantic-release/commit-analyzer";

/** Whether a commit that changed `files` belongs to the stream. */
export function belongs(files, { include = [], exclude = [] } = {}) {
  const under = (prefixes) => (file) => prefixes.some((p) => file.startsWith(p));
  if (include.length > 0 && !files.some(under(include))) return false;
  if (exclude.length > 0 && files.length > 0 && files.every(under(exclude))) return false;
  return true;
}

/** Whether every file of a commit is under one of `prefixes`. */
export function within(files, prefixes = []) {
  return prefixes.length > 0 && files.length > 0 && files.every((f) => prefixes.some((p) => f.startsWith(p)));
}

const RELEASES = [null, "patch", "minor", "major"];

/** The larger of two releases; null is none. */
const larger = (a, b) => (RELEASES.indexOf(a ?? null) >= RELEASES.indexOf(b ?? null) ? (a ?? null) : (b ?? null));

/** The files a commit changed. A merge changed none of its own. */
export function filesOf(hash, cwd) {
  const out = execFileSync("git", ["diff-tree", "--no-commit-id", "--name-only", "-r", "--root", hash], {
    cwd,
    encoding: "utf8",
  });
  return out.split("\n").filter(Boolean);
}

export async function analyzeCommits(pluginConfig, context) {
  return analyzeWith(pluginConfig, context, (hash) => filesOf(hash, context.cwd));
}

/** analyzeCommits with the files of each commit from `files`, for tests. */
export async function analyzeWith(pluginConfig, context, files) {
  const { include, exclude, modules, ...analyzerConfig } = pluginConfig;
  const commits = context.commits.filter((c) => belongs(files(c.hash), { include, exclude }));
  context.logger.log(`${commits.length} of ${context.commits.length} commits touch this release stream`);
  if (!modules) {
    return analyze(analyzerConfig, { ...context, commits });
  }
  const inModules = (c) => within(files(c.hash), modules.paths);
  const own = await analyze(analyzerConfig, { ...context, commits: commits.filter((c) => !inModules(c)) });
  const moduleConfig = {
    ...analyzerConfig,
    releaseRules: [...(analyzerConfig.releaseRules ?? []), ...modules.releaseRules],
  };
  const fromModules = await analyze(moduleConfig, { ...context, commits: commits.filter(inModules) });
  return larger(own, fromModules) ?? undefined;
}
