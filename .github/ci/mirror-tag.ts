// The Go module tag of each SDK release on HEAD: sdk/go/vX.Y.Z beside the
// sdk/python/vX.Y.Z semantic-release cut, on the same commit.
//
// Usage: node .github/ci/mirror-tag.ts
//
// Both SDKs release at one version (ADR-0043), and each consumer reads its
// own tag: the Go proxy resolves the module under its directory, hatch-vcs
// reads the Python package's. semantic-release cuts one tag per run, so this
// cuts the other.
//
// Safe to re-run, as a re-run of a failed release does: every stream tag on
// HEAD is mirrored, not only one this run pushed, so a run that failed after
// semantic-release and before this still gets its Go tag. A Go tag already on
// HEAD is left as it is. One on another commit fails the run: the release-tag
// ruleset forbids deleting a tag, so the two would name different code at one
// version for good, and someone has to decide which version comes next.

import { info } from "./lib/actions.ts";
import { git } from "./lib/git.ts";
import { runMain } from "./lib/main.ts";
import { goModuleTag, sdkStreams, streamOf, streams, tagPrefix } from "./lib/release-config.ts";
import { tagsOnHead } from "./release-tags.ts";

const REMOTE = "origin";

// commitOf is the commit tag points at, or undefined when there is no such tag.
async function commitOf(tag: string, root: string): Promise<string | undefined> {
  try {
    return (await git(["rev-parse", "--verify", "--quiet", `refs/tags/${tag}^{commit}`], root)).trim();
  } catch {
    return undefined;
  }
}

// mirrorTags tags and pushes the Go module tag of every SDK stream tag on
// HEAD in the clone at root that lacks one, and returns the tags it pushed.
export async function mirrorTags(root: string, remote: string = REMOTE): Promise<string[]> {
  const all = streams();
  const head = (await git(["rev-parse", "HEAD"], root)).trim();
  const onHead = await tagsOnHead(root);
  const pushed: string[] = [];
  for (const name of sdkStreams(all)) {
    const stream = all[name];
    if (stream === undefined) continue;
    const prefix = tagPrefix(stream);
    // The stream's own tags: its Go module tags are what this cuts.
    for (const tag of onHead.filter((t) => t.startsWith(prefix) && streamOf(t, all) === name)) {
      const module = goModuleTag(tag, stream);
      if (module === undefined) continue;
      const at = await commitOf(module, root);
      if (at === head) {
        info(`${module} is already on ${head}, beside ${tag}`);
        continue;
      }
      if (at !== undefined) {
        throw new Error(`${module} points at ${at}, but ${tag} is on ${head}: one version names two commits, and tags cannot be deleted`);
      }
      await git(["tag", module, head], root);
      await git(["push", "--quiet", remote, `refs/tags/${module}`], root);
      info(`tagged ${module} on ${head}, beside ${tag}`);
      pushed.push(module);
    }
  }
  if (pushed.length === 0) {
    info("no SDK release on HEAD lacks its Go module tag");
  }
  return pushed;
}

async function main(): Promise<void> {
  await mirrorTags((await git(["rev-parse", "--show-toplevel"])).trim());
}

if (import.meta.main) {
  await runMain(main);
}
