// The GitHub release of the product's tag, once every component's image and
// chart is in GHCR.
//
// Usage: TAG=<tag> GH_TOKEN=… GITHUB_REPOSITORY=… node announce.ts <sbom-dir>
//
// Notes for people, generated from the merged pull requests and shaped by
// .github/release.yml, with each image's per-platform SBOM attached. The same
// SBOMs are attested to the images; these copies are for reading.

import { readdirSync } from "node:fs";
import { join } from "node:path";

import { info, summarize } from "./lib/actions.ts";
import { requireEnv } from "./lib/env.ts";
import { gitHubFor, type GitHub } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";
import { isCandidate } from "./lib/versions.ts";
import { SBOM_SUFFIX } from "./sign.ts";

// sboms are the SBOM files in dir, in name order.
export function sboms(dir: string): string[] {
  return readdirSync(dir)
    .filter((f) => f.endsWith(SBOM_SUFFIX))
    .sort()
    .map((f) => join(dir, f));
}

// announce publishes the release of tag with the SBOMs in dir; one with none
// is refused, as a release missing what every release carries.
export async function announce(github: GitHub, tag: string, dir: string): Promise<void> {
  const assets = sboms(dir);
  if (assets.length === 0) {
    throw new Error(`no SBOM in ${dir}: refusing to announce ${tag} without them`);
  }
  // A release candidate is a prerelease, never the Latest: that stays the
  // last release people should install.
  const candidate = isCandidate(tag);
  await github.publishRelease({ tag, title: tag, notes: { generated: true }, latest: !candidate, prerelease: candidate, assets });
  info(`announced ${tag} with ${assets.length} SBOMs`);
}

export const ANNOUNCE_TITLE = "### The release";

// announceSummary is the summary page's link to the release it published.
export function announceSummary(tag: string, repository: string, sbomCount: number): string {
  const kind = isCandidate(tag) ? "a release candidate" : "the latest release";
  return `${ANNOUNCE_TITLE}\n\n[${tag}](https://github.com/${repository}/releases/tag/${tag}), ${kind}, with ${sbomCount} SBOMs.\n`;
}

async function main(): Promise<void> {
  const [dir] = process.argv.slice(2);
  if (dir === undefined) {
    throw new Error("usage: announce.ts <sbom-dir>");
  }
  const repository = requireEnv("GITHUB_REPOSITORY");
  const tag = requireEnv("TAG");
  await announce(gitHubFor(requireEnv("GH_TOKEN"), repository), tag, dir);
  summarize(announceSummary(tag, repository, sboms(dir).length));
}

if (import.meta.main) {
  await runMain(main);
}
