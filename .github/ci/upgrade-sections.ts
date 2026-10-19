// The upgrade guide's sections, named by the release that shipped them.
//
// Usage: GH_TOKEN=… GITHUB_REPOSITORY=… node upgrade-sections.ts name <tags-before file>
//        GH_TOKEN=<AUTOMERGE_TOKEN> GITHUB_REPOSITORY=… TAGS=<tags> node upgrade-sections.ts propose
//
// A section is written as "## Unreleased — <title>" with the change, because
// the version is not known until the release cuts it. name turns each such
// heading into "## <tags> — <title>", naming every tag of this run whose
// stream the section's change belongs to — product first, then the SDK, as
// in "## v26.0.0, sdk/go/v0.57.0 — …" — and sets the step output `tags`. The
// SDK's release notes link a section by that tag (sdk-release.ts).
//
// The change is the commit that added the heading, and its stream is decided
// the way paths-analyzer.mjs decides it, from release.config.cjs. A section
// written in a commit of documentation alone therefore reads as the
// product's: write it in the commit that makes the change, as the repository
// does anyway. A heading no tag of this run belongs to stays Unreleased.
//
// propose commits the renamed guide to a branch and opens a pull request:
// main is protected. The token must be AUTOMERGE_TOKEN, not GITHUB_TOKEN:
// GitHub starts no workflow for a pull request GITHUB_TOKEN opens, so CI
// would never report and it would never merge; auto-merge.yaml queues it once
// opened. The commit is docs, which releases nothing. Safe to re-run, as a
// re-run of a failed release does: the branch is named after the tags and
// force-pushed, and an open pull request for it is left as it is.

import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { belongs, deciding, filesOf } from "../release/paths-analyzer.mjs";
import { info, setOutput, warning } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { git, lines } from "./lib/git.ts";
import { gitHubFor, type GitHub } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";
import { streamOf, streams, type Stream } from "./lib/release-config.ts";
import { productTag, readBefore, streamTags } from "./release-tags.ts";

// The heading shape, which sdk-release.ts finds a section by.
export const HEADING = "## ";
export const UNRELEASED = "Unreleased";
export const SEPARATOR = " — ";
export const TAG_SEPARATOR = ", ";
export const UPGRADING = "docs/upgrading.md";
const UNRELEASED_HEADING = HEADING + UNRELEASED + SEPARATOR;

// OUTPUT_TAGS is the step output name sets: this run's tags, space-separated.
export const OUTPUT_TAGS = "tags";

// The pull request propose opens.
export const BASE = "main";
export const BRANCH_PREFIX = "docs/upgrading-";
const BOT = ["-c", "user.name=github-actions[bot]", "-c", "user.email=41898341+github-actions[bot]@users.noreply.github.com"];
const PR_BODY = "The release named the upgrade guide's Unreleased sections it shipped.";
// The checkout's own credential is GITHUB_TOKEN, sent as a header to every
// github.com URL; it would win over the token in the URL, so it is cleared.
const NO_CHECKOUT_CREDENTIAL = ["-c", "http.https://github.com/.extraheader="];

// Command is what the step asks for.
export const Command = { Name: "name", Propose: "propose" } as const;

// tagsFor is the tags among tags whose stream a commit that changed files
// belongs to, in stream order.
export function tagsFor(files: readonly string[], tags: readonly string[], all: Record<string, Stream>): string[] {
  const order = Object.keys(all);
  return tags
    .map((tag) => ({ tag, stream: streamOf(tag, all) }))
    .filter((t): t is { tag: string; stream: string } => {
      const stream = t.stream === undefined ? undefined : all[t.stream];
      if (stream === undefined) return false;
      const { documentation, ...filter } = stream.analyzer;
      return belongs(deciding(files, documentation), filter);
    })
    .sort((a, b) => order.indexOf(a.stream) - order.indexOf(b.stream))
    .map(({ tag }) => tag);
}

// nameSections is text with each Unreleased heading named by the tags
// tagsOf(heading) returns; none leaves it.
export function nameSections(text: string, tagsOf: (heading: string) => readonly string[]): string {
  return text
    .split("\n")
    .map((line) => {
      if (!line.startsWith(UNRELEASED_HEADING)) return line;
      const tags = tagsOf(line);
      return tags.length === 0 ? line : HEADING + tags.join(TAG_SEPARATOR) + SEPARATOR + line.slice(UNRELEASED_HEADING.length);
    })
    .join("\n");
}

// addedBy is the commit that added line to the upgrade guide: the oldest that
// changed how often it occurs.
async function addedBy(line: string, root: string): Promise<string | undefined> {
  return lines(await git(["log", "--format=%H", "--reverse", "-S", line, "--", UPGRADING], root))[0];
}

// nameGuide names the guide's sections in the clone at root for tags, and
// reports whether it changed.
export async function nameGuide(root: string, tags: readonly string[]): Promise<boolean> {
  const path = join(root, UPGRADING);
  const text = readFileSync(path, "utf8");
  const all = streams();
  const added = new Map<string, readonly string[]>();
  for (const line of text.split("\n").filter((l) => l.startsWith(UNRELEASED_HEADING))) {
    const commit = await addedBy(line, root);
    added.set(line, commit === undefined ? [] : tagsFor(filesOf(commit, root), tags, all));
  }
  const named = nameSections(text, (line) => added.get(line) ?? []);
  if (named === text) {
    return false;
  }
  writeFileSync(path, named);
  const before = text.split("\n");
  named.split("\n").forEach((line, i) => {
    if (line !== before[i]) info(line);
  });
  return true;
}

// guideChanged reports whether the guide in the clone at root differs from
// its commit.
export async function guideChanged(root: string): Promise<boolean> {
  return (await git(["status", "--porcelain", "--", UPGRADING], root)).trim() !== "";
}

// proposalTitle is the commit subject and the pull request title for tags.
export function proposalTitle(tags: readonly string[]): string {
  return `docs(upgrading): name the sections ${tags.join(TAG_SEPARATOR)} shipped`;
}

// propose commits the renamed guide in the clone at root to a branch named
// after tags, pushes it to remote, and opens its pull request unless one is
// open. Nothing renamed proposes nothing.
export async function propose(github: GitHub, root: string, tags: readonly string[], remote: string): Promise<void> {
  if (!(await guideChanged(root))) {
    info(`no upgrade section names ${tags.join(" ")}`);
    return;
  }
  const branch = BRANCH_PREFIX + tags.join("-");
  const title = proposalTitle(tags);
  await git(["switch", "--quiet", "-c", branch], root);
  await git(["add", "--", UPGRADING], root);
  await git([...BOT, "commit", "--quiet", "-m", title], root);
  await git([...NO_CHECKOUT_CREDENTIAL, "push", "--quiet", "--force", remote, `HEAD:refs/heads/${branch}`], root);
  if ((await github.openPullRequestFrom(branch)) !== undefined) {
    info(`a pull request for ${branch} is already open`);
    return;
  }
  info(`opened #${await github.createPullRequest(branch, BASE, title, PR_BODY)}`);
}

// splitTags reads the space-separated tags of the TAGS variable.
export function splitTags(text: string): string[] {
  return text.split(/\s+/).filter((t) => t !== "");
}

async function main(): Promise<void> {
  const [command, file] = process.argv.slice(2);
  const repository = requireEnv("GITHUB_REPOSITORY");
  const root = (await git(["rev-parse", "--show-toplevel"])).trim();
  switch (command) {
    case Command.Name: {
      if (file === undefined) throw new Error(`usage: upgrade-sections.ts ${Command.Name} <tags-before file>`);
      const github = gitHubFor(requireEnv("GH_TOKEN"), repository);
      const before = readBefore(file);
      const product = await productTag(github, before, root);
      const tags = [...(product === undefined ? [] : [product]), ...(await streamTags(github, before, root))];
      if (tags.length === 0) {
        info("no tag from this run");
        return;
      }
      await nameGuide(root, tags);
      setOutput(OUTPUT_TAGS, tags.join(" "));
      return;
    }
    case Command.Propose: {
      const tags = splitTags(requireEnv("TAGS"));
      const token = optionalEnv("GH_TOKEN");
      if (!(await guideChanged(root))) {
        info(`no upgrade section names ${tags.join(" ")}`);
        return;
      }
      if (token === undefined) {
        warning(`AUTOMERGE_TOKEN is not set; name the sections for ${tags.join(" ")} by hand`);
        return;
      }
      await propose(gitHubFor(token, repository), root, tags, `https://x-access-token:${token}@github.com/${repository}.git`);
      return;
    }
    default:
      throw new Error(`usage: upgrade-sections.ts ${Command.Name}|${Command.Propose}`);
  }
}

if (import.meta.main) {
  await runMain(main);
}
