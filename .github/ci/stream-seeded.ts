// Whether a release stream has a tag to version from.
//
// Usage: RELEASE_STREAM=<stream> node stream-seeded.ts
//
// Sets the step output `seeded` to true when the stream has at least one
// release tag, and false, with a warning, when it has none. semantic-release
// starts a stream with no tag at 1.0.0, and the release-tag ruleset forbids
// deleting a tag, so a stream's first tag is cut by hand at the version it
// continues from (docs/releasing.md); until then its release is skipped.

import { setOutput, warning } from "./lib/actions.ts";
import { requireEnv } from "./lib/env.ts";
import { git, lines } from "./lib/git.ts";
import { runMain } from "./lib/main.ts";
import { streamOf, streams, tagPrefix } from "./lib/release-config.ts";

// OUTPUT_SEEDED is the step output this sets.
export const OUTPUT_SEEDED = "seeded";

// seeded reports whether the clone at cwd holds a release tag of the stream.
export async function seeded(name: string, cwd?: string): Promise<boolean> {
  const all = streams();
  const stream = all[name];
  if (stream === undefined) {
    throw new Error(`release.config.cjs has no ${name} stream`);
  }
  const tags = lines(await git(["tag", "--list", `${tagPrefix(stream)}*`], cwd));
  return tags.some((tag) => streamOf(tag, all) === name);
}

async function main(): Promise<void> {
  const name = requireEnv("RELEASE_STREAM");
  const ok = await seeded(name);
  if (!ok) {
    warning(`the ${name} stream has no release tag yet; cut its first by hand (docs/releasing.md) — not releasing it`);
  }
  setOutput(OUTPUT_SEEDED, String(ok));
}

if (import.meta.main) {
  await runMain(main);
}
