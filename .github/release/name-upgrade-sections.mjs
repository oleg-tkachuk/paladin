// name-upgrade-sections.mjs — puts the release's tags into the
// docs/upgrading.md sections it shipped.
//
// Usage: node name-upgrade-sections.mjs <tag>…   the tags this release run cut
//
// A section is written as "## Unreleased — <title>" with the change, because
// the version is not known until the release cuts it. Here each such heading
// becomes "## <tags> — <title>", naming every tag of this run whose stream the
// section's change belongs to — product first, then the SDK, as in
// "## v26.0.0, sdk/go/v0.57.0 — …". scripts/stream-release-notes.sh finds a
// section by that tag, and links it from the release notes.
//
// The change is the commit that added the heading, and its stream is decided
// the way paths-analyzer.mjs decides it, from release.config.cjs. A section
// written in a commit of documentation alone therefore reads as the
// product's: write it in the commit that makes the change, as the repository
// does anyway. A heading no tag of this run belongs to stays Unreleased.
//
// The heading shape is a contract with stream-release-notes.sh, which matches
// "## <tags> — " in bash and cannot import these.

import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { belongs, deciding, filesOf } from "./paths-analyzer.mjs";

export const HEADING = "## ";
export const UNRELEASED = "Unreleased";
export const SEPARATOR = " — ";
export const TAG_SEPARATOR = ", ";
export const UPGRADING = "docs/upgrading.md";

const UNRELEASED_HEADING = HEADING + UNRELEASED + SEPARATOR;
const VERSION = /^\d+\.\d+\.\d+$/;

/** The name of the stream `tag` is a release of, or undefined. */
export function streamOf(tag, streams) {
  for (const [name, s] of Object.entries(streams)) {
    const prefix = s.tagFormat.replace("${version}", "");
    if (tag.startsWith(prefix) && VERSION.test(tag.slice(prefix.length))) return name;
  }
  return undefined;
}

/** The tags among `tags` whose stream a commit that changed `files` belongs to, in stream order. */
export function tagsFor(files, tags, streams) {
  const order = Object.keys(streams);
  return tags
    .map((tag) => ({ tag, stream: streamOf(tag, streams) }))
    .filter(({ stream }) => {
      if (!stream) return false;
      const { documentation, ...analyzer } = streams[stream].analyzer;
      return belongs(deciding(files, documentation), analyzer);
    })
    .sort((a, b) => order.indexOf(a.stream) - order.indexOf(b.stream))
    .map(({ tag }) => tag);
}

/** `text` with each Unreleased heading named by the tags `tagsOf(heading)` returns; none leaves it. */
export function nameSections(text, tagsOf) {
  return text
    .split("\n")
    .map((line) => {
      if (!line.startsWith(UNRELEASED_HEADING)) return line;
      const tags = tagsOf(line);
      if (tags.length === 0) return line;
      return HEADING + tags.join(TAG_SEPARATOR) + SEPARATOR + line.slice(UNRELEASED_HEADING.length);
    })
    .join("\n");
}

/** The commit that added `line` to the upgrade guide: the oldest that changed how often it occurs. */
function addedBy(line, cwd) {
  const out = execFileSync("git", ["log", "--format=%H", "--reverse", "-S", line, "--", UPGRADING], {
    cwd,
    encoding: "utf8",
  });
  return out.split("\n").find(Boolean);
}

function main(tags) {
  const require = createRequire(import.meta.url);
  const { streams } = require("./release.config.cjs");
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
  const path = join(root, UPGRADING);
  const text = readFileSync(path, "utf8");
  const named = nameSections(text, (line) => {
    const commit = addedBy(line, root);
    return commit ? tagsFor(filesOf(commit, root), tags, streams) : [];
  });
  if (named === text) return;
  writeFileSync(path, named);
  const before = text.split("\n");
  named.split("\n").forEach((line, i) => {
    if (line !== before[i]) console.log(line);
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
