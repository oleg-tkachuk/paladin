// The release tags this run pushed, if any.
//
// Usage: node release-tags.ts before <file>     note the tags on HEAD before semantic-release
//        GH_TOKEN=… GITHUB_REPOSITORY=… node release-tags.ts product <file>
//
// product sets the step output `tag` to the product tag (v*) on HEAD that is
// not in the file, or to nothing. "A v* tag on HEAD" alone is not the same
// question: release.yaml is dispatched once per CI run on main and checks out
// the branch head, so two dispatches can land on one commit. The second found
// the first run's tag on HEAD, took it for its own, and failed creating a
// release that already existed.
//
// A tag already on HEAD whose GitHub release does not exist is reported too,
// when no tag is new: a run that pushed the tag and failed before publishing
// it. semantic-release does not tag twice, so re-running that run finds no new
// tag; this is how the re-run publishes what the first one left behind. A tag
// with its release is a finished release and stays out, so a second dispatch
// to the same commit still publishes nothing.
//
// The SDKs' tags are the stream tags (sdk/go/v*, sdk/python/v*), read the
// same way by streamTags. The api/vX.Y.Z proto baselines can point at the same
// commit and are not releases; nor are the capability/v* tags of a module
// that moved to its own repository.

import { readFileSync, writeFileSync } from "node:fs";

import { info, setOutput } from "./lib/actions.ts";
import { requireEnv } from "./lib/env.ts";
import { git, lines } from "./lib/git.ts";
import { gitHubFor, type GitHub } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";
import { PRODUCT, sdkStreams, streams, tagPrefix } from "./lib/release-config.ts";

// OUTPUT_TAG is the step output product sets.
export const OUTPUT_TAG = "tag";

// Command is what the step asks for.
export const Command = { Before: "before", Product: "product" } as const;

// tagsOnHead lists the tags pointing at HEAD.
export async function tagsOnHead(cwd?: string): Promise<string[]> {
  return lines(await git(["tag", "--points-at", "HEAD"], cwd));
}

// RELEASE_VERSION is what follows a stream's prefix in one of its tags.
const RELEASE_VERSION = /^[0-9]/;

// ofStream keeps the tags of the stream whose tags start with prefix.
const ofStream = (tags: readonly string[], prefix: string) => tags.filter((t) => t.startsWith(prefix) && RELEASE_VERSION.test(t.slice(prefix.length)));

// pendingTags are the stream's tags on HEAD that are not in before or, when
// none is, the ones that have no GitHub release.
export async function pendingTags(github: GitHub, onHead: readonly string[], before: readonly string[], prefix: string): Promise<string[]> {
  const mine = ofStream(onHead, prefix);
  const fresh = mine.filter((t) => !before.includes(t));
  if (fresh.length > 0) {
    return fresh;
  }
  const unpublished: string[] = [];
  for (const tag of mine) {
    if (!(await github.releaseExists(tag))) {
      unpublished.push(tag);
    }
  }
  return unpublished;
}

// readBefore is the list of tags the before command noted.
export function readBefore(file: string): string[] {
  return lines(readFileSync(file, "utf8"));
}

// productTag is the product tag this run pushed, or undefined.
export async function productTag(github: GitHub, before: readonly string[], cwd?: string): Promise<string | undefined> {
  const product = streams()[PRODUCT];
  if (product === undefined) {
    throw new Error(`release.config.cjs has no ${PRODUCT} stream`);
  }
  return (await pendingTags(github, await tagsOnHead(cwd), before, tagPrefix(product)))[0];
}

// streamTags are the SDK tags this run pushed, of every SDK stream.
export async function streamTags(github: GitHub, before: readonly string[], cwd?: string): Promise<string[]> {
  const all = streams();
  const onHead = await tagsOnHead(cwd);
  const tags: string[] = [];
  for (const name of sdkStreams(all)) {
    const stream = all[name];
    if (stream !== undefined) {
      tags.push(...(await pendingTags(github, onHead, before, tagPrefix(stream))));
    }
  }
  return tags;
}

async function main(): Promise<void> {
  const [command, file] = process.argv.slice(2);
  if (file === undefined || (command !== Command.Before && command !== Command.Product)) {
    throw new Error(`usage: release-tags.ts ${Command.Before}|${Command.Product} <file>`);
  }
  if (command === Command.Before) {
    writeFileSync(file, (await tagsOnHead()).map((t) => `${t}\n`).join(""));
    return;
  }
  const tag = await productTag(gitHubFor(requireEnv("GH_TOKEN"), requireEnv("GITHUB_REPOSITORY")), readBefore(file));
  if (tag === undefined) {
    info("no new tag from this run — no release was due");
    return;
  }
  setOutput(OUTPUT_TAG, tag);
}

if (import.meta.main) {
  await runMain(main);
}
