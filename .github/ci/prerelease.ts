// Tag a release candidate of the product: the version its next release
// would be, as vX.Y.Z-rc.N on the checked-out commit. By hand only.
//
// Usage: node .github/ci/prerelease.ts
//
// The version is semantic-release's own answer, asked in dry-run mode with
// the product stream's config, so a candidate is always the release that
// would follow it. N is one past the highest candidate already tagged for
// that version. Nothing is tagged when no release is due.
//
// The tag is the candidate: the steps after this one read it like any tag
// this run pushed, publish its images and charts at X.Y.Z-rc.N, and announce
// it as a prerelease that is never the Latest. semantic-release on main counts
// only stable tags, so the release after the candidates is still X.Y.Z.

import { createRequire } from "node:module";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import { info, withoutWorkflowCommands } from "./lib/actions.ts";
import { git, lines } from "./lib/git.ts";
import { runMain } from "./lib/main.ts";
import { PRODUCT, streams, tagPrefix } from "./lib/release-config.ts";
import { RC } from "./lib/versions.ts";

// RELEASE_DIR holds the release tooling and its config, relative to the root.
const RELEASE_DIR = ".github/release";
// STREAM_ENV picks the stream release.config.cjs configures.
const STREAM_ENV = "RELEASE_STREAM";
const REMOTE = "origin";

// candidateTag is the tag of the next candidate of version, given the tags
// the repository holds: one past the highest -rc.N for it.
export function candidateTag(prefix: string, version: string, tags: readonly string[]): string {
  const base = `${prefix}${version}-${RC}.`;
  const taken = tags
    .filter((t) => t.startsWith(base))
    .map((t) => t.slice(base.length))
    .filter((n) => /^[0-9]+$/.test(n))
    .map(Number);
  return `${base}${Math.max(0, ...taken) + 1}`;
}

// NextVersion answers the version the product's next release would take,
// or undefined when none is due.
export type NextVersion = (root: string) => Promise<string | undefined>;

interface SemanticResult {
  nextRelease?: { version: string };
}
type SemanticRelease = (options: Record<string, unknown>, config: { cwd: string }) => Promise<SemanticResult | false>;

// dryRunVersion asks the pinned semantic-release, in dry-run mode, from the
// release tooling's directory, as the release step runs it.
export const dryRunVersion: NextVersion = async (root) => {
  const dir = join(root, RELEASE_DIR);
  process.env[STREAM_ENV] = PRODUCT;
  const require = createRequire(join(dir, "package.json"));
  const { default: semanticRelease } = (await import(pathToFileURL(require.resolve("semantic-release")).href)) as { default: SemanticRelease };
  // It logs every commit message it reads, in a job holding a write token:
  // no line of that may act as a workflow command.
  const result = await withoutWorkflowCommands(() => semanticRelease({ dryRun: true, ci: false }, { cwd: dir }));
  return result === false ? undefined : result.nextRelease?.version;
};

// tagCandidate tags and pushes the next candidate in the clone at root, and
// returns its tag; undefined when no release is due.
export async function tagCandidate(root: string, next: NextVersion = dryRunVersion, remote: string = REMOTE): Promise<string | undefined> {
  const product = streams()[PRODUCT];
  if (product === undefined) {
    throw new Error(`release.config.cjs has no ${PRODUCT} stream`);
  }
  const version = await next(root);
  if (version === undefined) {
    info("no release is due: nothing to cut a candidate of");
    return undefined;
  }
  const tag = candidateTag(tagPrefix(product), version, lines(await git(["tag", "--list"], root)));
  await git(["tag", tag, "HEAD"], root);
  await git(["push", "--quiet", remote, `refs/tags/${tag}`], root);
  info(`tagged ${tag}`);
  return tag;
}

async function main(): Promise<void> {
  await tagCandidate((await git(["rev-parse", "--show-toplevel"])).trim());
}

if (import.meta.main) {
  await runMain(main);
}
