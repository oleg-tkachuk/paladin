// paths-analyzer.mjs — a semantic-release analyzeCommits plugin that decides a
// release from the commits touching one part of the repository.
//
// The bump itself is @semantic-release/commit-analyzer's, with the options it
// is given. This only chooses which commits it reads, by the files each one
// changed: `include` keeps a commit that touches any of those prefixes,
// `exclude` drops one whose every file is under them. So the SDK releases for
// a change to proto/ whatever its commit's scope, and the product does not
// release for a commit that only touched the SDK.

import { execFileSync } from "node:child_process";
import { analyzeCommits as analyze } from "@semantic-release/commit-analyzer";

/** Whether a commit that changed `files` belongs to the stream. */
export function belongs(files, { include = [], exclude = [] } = {}) {
  const under = (prefixes) => (file) => prefixes.some((p) => file.startsWith(p));
  if (include.length > 0 && !files.some(under(include))) return false;
  if (exclude.length > 0 && files.length > 0 && files.every(under(exclude))) return false;
  return true;
}

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
  const { include, exclude, ...analyzerConfig } = pluginConfig;
  const commits = context.commits.filter((c) => belongs(files(c.hash), { include, exclude }));
  context.logger.log(`${commits.length} of ${context.commits.length} commits touch this release stream`);
  return analyze(analyzerConfig, { ...context, commits });
}
