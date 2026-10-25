// The upgrade guide's sections, named by the release that shipped them.
//
// Usage: GH_TOKEN=… GITHUB_REPOSITORY=… node upgrade-sections.ts name <tags-before file>
//        GH_TOKEN=<AUTOMERGE_TOKEN> GITHUB_REPOSITORY=… TAGS=<tags> node upgrade-sections.ts propose
//
// A section is a file of its own, docs/upgrading/<slug>.md, headed
// "## Unreleased — <title>" and written with the change, because the version
// is not known until the release cuts it. A pull request only adds such a
// file; only the release writes docs/upgrading.md, between its markers, from
// the files, newest first. A pull request that edited the guide itself
// conflicted with every release that named a section beside its own. The
// sections written into the guide before the files existed stay below the
// markers, and are named there as before. name turns each such
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
// A change can belong to the product and an SDK alike, and the streams
// release apart: the first to release names the section, and the other adds
// its tag when it releases in turn. It adds it only when no earlier tag of its
// stream holds the change, so a section shipped before keeps its name.
//
// propose commits the renamed guide to a branch and opens a pull request:
// main is protected. The token must be AUTOMERGE_TOKEN, not GITHUB_TOKEN:
// GitHub starts no workflow for a pull request GITHUB_TOKEN opens, so CI
// would never report and it would never merge; auto-merge.yaml queues it once
// opened. The commit is docs, which releases nothing. Safe to re-run, as a
// re-run of a failed release does: the branch is named after the tags and
// force-pushed, and an open pull request for it is left as it is.

import { existsSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
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
// SECTIONS is the directory of the section files, and SECTION_EXTENSION what
// a section file's name ends with.
export const SECTIONS = "docs/upgrading";
export const SECTION_EXTENSION = ".md";
// The markers the release writes the section files between, in the guide.
export const SECTIONS_BEGIN = "<!-- sections: written by the release from docs/upgrading/, newest first; add a file there instead -->";
export const SECTIONS_END = "<!-- end of sections -->";
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

// Section is a heading of the guide: the tags that named it, none while it
// is Unreleased, and its title.
export interface Section {
  tags: string[];
  title: string;
}

// parseSection reads line as a section heading, or undefined when it is not
// one: a heading of the guide's own, such as its introduction.
export function parseSection(line: string, all: Record<string, Stream>): Section | undefined {
  if (!line.startsWith(HEADING)) return undefined;
  const at = line.indexOf(SEPARATOR);
  if (at < 0) return undefined;
  const name = line.slice(HEADING.length, at);
  const title = line.slice(at + SEPARATOR.length);
  if (name === UNRELEASED) return { tags: [], title };
  const tags = name.split(TAG_SEPARATOR);
  return tags.every((t) => streamOf(t, all) !== undefined) ? { tags, title } : undefined;
}

// unreleasedHeading is the heading section was written with.
export function unreleasedHeading(section: Section): string {
  return UNRELEASED_HEADING + section.title;
}

// nameSections is text with each section's heading given the tags
// tagsOf(heading) adds to it, every tag in stream order; none leaves it.
export function nameSections(text: string, tagsOf: (heading: string) => readonly string[], all: Record<string, Stream>): string {
  const order = Object.keys(all);
  const rank = (tag: string) => order.indexOf(streamOf(tag, all) ?? "");
  return text
    .split("\n")
    .map((line) => {
      const section = parseSection(line, all);
      if (section === undefined) return line;
      const added = tagsOf(line).filter((t) => !section.tags.includes(t));
      if (added.length === 0) return line;
      const tags = [...section.tags, ...added].sort((a, b) => rank(a) - rank(b));
      return HEADING + tags.join(TAG_SEPARATOR) + SEPARATOR + section.title;
    })
    .join("\n");
}

// addedBy is the commit that added line to the upgrade guide: the oldest that
// changed how often it occurs.
async function addedBy(line: string, root: string): Promise<string | undefined> {
  return lines(await git(["log", "--format=%H", "--reverse", "-S", line, "--", UPGRADING], root))[0];
}

// shippedBefore reports whether a tag of tag's stream other than the run's
// tags already holds commit: the stream released the change before.
async function shippedBefore(commit: string, tag: string, tags: readonly string[], root: string, all: Record<string, Stream>): Promise<boolean> {
  const stream = streamOf(tag, all);
  return lines(await git(["tag", "--contains", commit], root)).some((t) => !tags.includes(t) && streamOf(t, all) === stream);
}

// filesAdded is, for each section file, the commit that added it and its
// age: 0 for the newest, counting back through history, which orders files
// added in the same second as surely as any others.
async function filesAdded(root: string): Promise<Map<string, { commit: string; age: number }>> {
  const out = new Map<string, { commit: string; age: number }>();
  let commit = "";
  let age = -1;
  for (const line of lines(await git(["log", "--diff-filter=A", "--format=%x00%H", "--name-only", "--", SECTIONS], root))) {
    if (line.startsWith("\0")) {
      commit = line.slice(1);
      age += 1;
    } else if (line !== "" && !out.has(line)) {
      out.set(line, { commit, age });
    }
  }
  return out;
}

// sectionTags is the tags of this run section's change belongs to and its
// heading still lacks: of a stream not in it, which has not shipped it
// before. The change is commit, or the one that wrote the heading into the
// guide when there is none.
async function sectionTags(section: Section, root: string, tags: readonly string[], all: Record<string, Stream>, commit?: string): Promise<string[]> {
  const named = new Set(section.tags.map((t) => streamOf(t, all)));
  const missing = tags.filter((t) => !named.has(streamOf(t, all)));
  if (missing.length === 0) return [];
  commit ??= await addedBy(unreleasedHeading(section), root);
  if (commit === undefined) return [];
  const wanted = tagsFor(filesOf(commit, root), missing, all);
  if (section.tags.length === 0) return wanted;
  const fresh: string[] = [];
  for (const tag of wanted) {
    if (!(await shippedBefore(commit, tag, tags, root, all))) fresh.push(tag);
  }
  return fresh;
}

// SectionFile is one section file: its path from the root, its text, and
// the age of the commit that added it, 0 the newest.
export interface SectionFile {
  path: string;
  text: string;
  age: number;
}

// sectionsBlock is files as the guide carries them between its markers:
// newest first, each as written.
export function sectionsBlock(files: readonly SectionFile[]): string {
  return [...files]
    .sort((a, b) => a.age - b.age || a.path.localeCompare(b.path))
    .map((f) => f.text.trim())
    .join("\n\n");
}

// withSections is guide with block between its markers, or guide as it is
// when it has none.
export function withSections(guide: string, block: string): string {
  const begin = guide.indexOf(SECTIONS_BEGIN);
  const end = guide.indexOf(SECTIONS_END);
  if (begin < 0 || end < begin) return guide;
  const body = block === "" ? "\n" : `\n\n${block}\n\n`;
  return guide.slice(0, begin + SECTIONS_BEGIN.length) + body + guide.slice(end);
}

// outsideSections is guide without what lies between its markers: the part
// whose headings the guide itself holds.
function outsideSections(guide: string): string {
  return withSections(guide, "");
}

// nameFiles names the sections of the files under SECTIONS in the clone at
// root for tags, and returns every file, as named.
async function nameFiles(root: string, tags: readonly string[], all: Record<string, Stream>): Promise<{ files: SectionFile[]; changed: boolean }> {
  const dir = join(root, SECTIONS);
  if (!existsSync(dir)) return { files: [], changed: false };
  const files: SectionFile[] = [];
  const additions = await filesAdded(root);
  let changed = false;
  for (const name of readdirSync(dir).filter((n) => n.endsWith(SECTION_EXTENSION)).sort()) {
    const path = `${SECTIONS}/${name}`;
    const text = readFileSync(join(root, path), "utf8");
    const added = additions.get(path);
    const headings = new Map<string, readonly string[]>();
    for (const line of text.split("\n")) {
      const section = parseSection(line, all);
      if (section !== undefined) headings.set(line, await sectionTags(section, root, tags, all, added?.commit));
    }
    const named = nameSections(text, (line) => headings.get(line) ?? [], all);
    if (named !== text) {
      writeFileSync(join(root, path), named);
      changed = true;
    }
    // A file not yet committed is the newest of all.
    files.push({ path, text: named, age: added?.age ?? -1 });
  }
  return { files, changed };
}

// nameGuide names the sections in the clone at root for tags — the files
// under SECTIONS, then the ones the guide itself holds — writes the files
// into the guide between its markers, and reports whether anything changed.
export async function nameGuide(root: string, tags: readonly string[]): Promise<boolean> {
  const path = join(root, UPGRADING);
  const text = readFileSync(path, "utf8");
  const all = streams();
  const { files, changed: filesChanged } = await nameFiles(root, tags, all);
  const added = new Map<string, readonly string[]>();
  for (const line of outsideSections(text).split("\n")) {
    const section = parseSection(line, all);
    if (section !== undefined) added.set(line, await sectionTags(section, root, tags, all));
  }
  const named = withSections(nameSections(outsideSections(text), (line) => added.get(line) ?? [], all), sectionsBlock(files));
  if (named !== text) {
    writeFileSync(path, named);
    const before = text.split("\n");
    named.split("\n").forEach((line, i) => {
      if (line !== before[i]) info(line);
    });
  }
  return filesChanged || named !== text;
}

// guideChanged reports whether the guide or a section file in the clone at
// root differs from its commit.
export async function guideChanged(root: string): Promise<boolean> {
  return (await git(["status", "--porcelain", "--", UPGRADING, SECTIONS], root)).trim() !== "";
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
  await git(["add", "--", UPGRADING, ...(existsSync(join(root, SECTIONS)) ? [SECTIONS] : [])], root);
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
