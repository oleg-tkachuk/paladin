// The GitHub release beside each SDK tag this run cut, and the Go proxy told
// of it.
//
// Usage: GH_TOKEN=… GITHUB_REPOSITORY=… node sdk-release.ts <tags-before file>
//
// The notes list the feat, fix, perf, security and revert commits since the
// stream's previous tag that touch the stream's own files, and a compare
// link. A breaking commit — `!` after the type, or a BREAKING CHANGE footer —
// is listed first, under Behaviour changes, with a link to its section of
// docs/upgrading.md: pre-1.0 it is only a minor, so the notes are where it
// shows. GitHub's generated notes would list every pull request between two
// tags, most of them the product's. The stream, its tag format and its paths
// come from release.config.cjs, the file the tag was cut with. Never the
// latest release: that stays the product's.
//
// proxy.golang.org learns of a version when someone asks for it, and until
// then its @latest — which `go get` and the README's SDK badge read — keeps
// answering the previous one. Asking for the version's .info, the endpoint
// `go list -m module@version` would ask, makes it fetch the tag now. The
// release stands without it.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { info, warning } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { git, lines } from "./lib/git.ts";
import { gitHubFor, type GitHub } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";
import { retry } from "./lib/retry.ts";
import { SDK, streamOf, streams, tagPrefix } from "./lib/release-config.ts";
import { readBefore, streamTags } from "./release-tags.ts";
import { HEADING, UPGRADING } from "./upgrade-sections.ts";

// TITLES are the streams that get notes of their own, by the name a release
// is titled with.
const TITLES: Readonly<Record<string, string>> = { [SDK]: "SDK" };
// The released commit types, as the stream's bump counts them; the rest
// (docs, ci, chore, …) release nothing and are left out.
const FEATURES = /^feat(\([^)]*\))?!?: /;
const FIXES = /^(fix|perf|security|revert)(\([^)]*\))?!?: /;
const BANG = /^[a-z]+(\([^)]*\))?!: /;
const FOOTER = /^BREAKING[ -]CHANGE: /m;
// DEFAULT_BRANCH holds the upgrade guide a breaking release links to: its
// heading is named after the tag is cut (upgrade-sections.ts), so the tag's
// own copy still says Unreleased.
const DEFAULT_BRANCH = "main";

// The Go module proxy, and how often it is asked: it fetches from GitHub on
// the first request.
export const GO_PROXY = "https://proxy.golang.org";
export const PROXY_ATTEMPTS = 5;
export const PROXY_PAUSE_MS = 10_000;
const MODULE_TAG = /^(.+)\/(v[0-9]+\.[0-9]+\.[0-9]+)$/;

// Notes are a release's title and its Markdown body.
export interface Notes {
  title: string;
  body: string;
}

// anchor is GitHub's anchor for a heading: lower case, punctuation dropped,
// spaces to hyphens.
export function anchor(heading: string): string {
  return heading
    .toLowerCase()
    .replace(/[^a-z0-9 _-]/g, "")
    .replaceAll(" ", "-");
}

const bullets = (items: readonly string[]) => items.map((i) => `- ${i}`).join("\n");
const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// upgradeHeading is the guide's heading that names tag, without its "## ".
export function upgradeHeading(guide: string, tag: string): string | undefined {
  const named = new RegExp(`^${escapeRegExp(HEADING)}(.*[ ,])?${escapeRegExp(tag)}([ ,]|$)`);
  return guide
    .split("\n")
    .find((line) => named.test(line))
    ?.slice(HEADING.length);
}

// streamNotes is the notes of the stream tag in the clone at root, of
// repository owner/name.
export async function streamNotes(tag: string, root: string, repository: string): Promise<Notes> {
  const all = streams();
  const name = streamOf(tag, all);
  const stream = name === undefined ? undefined : all[name];
  const title = name === undefined ? undefined : TITLES[name];
  if (stream === undefined || title === undefined) {
    throw new Error(`${tag} is not an SDK tag`);
  }
  const prefix = tagPrefix(stream);
  const include = stream.analyzer.include ?? [];
  let previous: string | undefined;
  try {
    previous = (await git(["describe", "--tags", "--abbrev=0", "--match", `${prefix}*`, `${tag}^`], root)).trim();
  } catch {
    previous = undefined;
  }
  const heading = `${title} ${tag.slice(prefix.length)}`;
  if (previous === undefined) {
    return { title: heading, body: "The first release of this stream.\n" };
  }

  const breaking: string[] = [];
  const log: string[] = [];
  for (const hash of lines(await git(["log", "--no-merges", "--format=%H", `${previous}..${tag}`, "--", ...include], root))) {
    const line = (await git(["log", "-1", "--format=%s (%h)", hash], root)).trim();
    const body = await git(["log", "-1", "--format=%B", hash], root);
    (BANG.test(line) || FOOTER.test(body) ? breaking : log).push(line);
  }
  const features = log.filter((l) => FEATURES.test(l));
  const fixes = log.filter((l) => FIXES.test(l));

  const sections: string[] = [];
  if (breaking.length > 0) {
    let guide = "";
    try {
      guide = readFileSync(join(root, UPGRADING), "utf8");
    } catch {
      guide = "";
    }
    const named = upgradeHeading(guide, tag);
    const migrating =
      named === undefined
        ? `Migrating: [${UPGRADING}](https://github.com/${repository}/blob/${tag}/${UPGRADING})`
        : `Migrating: [${named}](https://github.com/${repository}/blob/${DEFAULT_BRANCH}/${UPGRADING}#${anchor(named)})`;
    sections.push(`## Behaviour changes\n\n${bullets(breaking)}\n\n${migrating}`);
  }
  if (features.length > 0) sections.push(`## Features\n\n${bullets(features)}`);
  if (fixes.length > 0) sections.push(`## Fixes\n\n${bullets(fixes)}`);
  if (sections.length === 0) sections.push(`No feature or fix commits touched ${include.join(" ")} in this release.`);
  sections.push(`**Full changes:** https://github.com/${repository}/compare/${previous}...${tag}`);
  return { title: heading, body: `${sections.join("\n\n")}\n` };
}

// moduleInfoUrl is the proxy URL whose answer makes it fetch the module tag
// names: this repository's path plus the tag's directory, at its version.
export function moduleInfoUrl(tag: string, repository: string, proxy: string = GO_PROXY): string {
  const match = MODULE_TAG.exec(tag);
  if (match === null || match[1] === undefined || match[2] === undefined) {
    throw new Error(`${tag} is not a module tag (<dir>/vX.Y.Z)`);
  }
  const module = `github.com/${repository}/${match[1]}`;
  // The protocol escapes upper-case letters in a module path; this
  // repository's paths are lower-case, and this keeps them so.
  if (/[A-Z]/.test(module)) {
    throw new Error(`${module} has upper-case letters, which the proxy protocol escapes`);
  }
  return `${proxy}/${module}/@v/${match[2]}.info`;
}

// warmGoProxy asks the proxy for the tag's version until it answers.
export async function warmGoProxy(url: string, pauseMs: number = PROXY_PAUSE_MS): Promise<string> {
  return retry(
    `asking ${url}`,
    async () => {
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error(`${response.status} ${response.statusText}`);
      }
      return response.text();
    },
    { attempts: PROXY_ATTEMPTS, delayMs: pauseMs },
  );
}

// publishStream publishes the release of one stream tag and tells the proxy.
export async function publishStream(github: GitHub, tag: string, root: string, repository: string, proxy: string = GO_PROXY, pauseMs?: number): Promise<void> {
  const notes = await streamNotes(tag, root, repository);
  await github.publishRelease({ tag, title: notes.title, notes: { text: notes.body }, latest: false, assets: [] });
  info(`released ${tag}`);
  try {
    info(await warmGoProxy(moduleInfoUrl(tag, repository, proxy), pauseMs));
  } catch (err) {
    warning(`the Go proxy did not take ${tag} yet; it will on its own: ${err instanceof Error ? err.message : String(err)}`);
  }
}

async function main(): Promise<void> {
  const [file] = process.argv.slice(2);
  if (file === undefined) {
    throw new Error("usage: sdk-release.ts <tags-before file>");
  }
  const repository = requireEnv("GITHUB_REPOSITORY");
  const github = gitHubFor(requireEnv("GH_TOKEN"), repository);
  const root = (await git(["rev-parse", "--show-toplevel"])).trim();
  for (const tag of await streamTags(github, readBefore(file), root)) {
    await publishStream(github, tag, root, repository, optionalEnv("GOPROXY_URL") ?? GO_PROXY);
  }
}

if (import.meta.main) {
  await runMain(main);
}
