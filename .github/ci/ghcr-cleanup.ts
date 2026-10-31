// What a dev publish removes from GHCR afterwards.
//
// Usage: PACKAGE=<image package> CHART_PACKAGE=<chart package> node ghcr-cleanup.ts
//
// Every green merge to main publishes a dev build: an image and a chart, each
// with its platform manifests, signatures and SBOM attestations. Nothing
// removed them, and GitHub has no retention setting for its container
// registry. So the publish that adds a dev build deletes the older ones,
// keeping the newest few. Releases are never matched: their versions carry no
// dev mark.
//
// Set as step outputs for dataaxiom/ghcr-cleanup-action, which deletes a
// version with its children and keeps those of what it keeps.

import { setOutput, summarize } from "./lib/actions.ts";
import { requireEnv } from "./lib/env.ts";
import { runMain } from "./lib/main.ts";
import { DEV_MARKS } from "./lib/versions.ts";

// DEV_BUILDS_KEPT is how many dev builds each package keeps. Not just the
// newest: the dev cluster deploys a build some minutes after it is published
// and runs it until the next one lands, so a build replaced in GHCR may still
// be mid-deploy or running. Keeping only one deleted the image a sync was
// pulling the moment the next merge published (three merges within ten
// minutes did it on 2026-10-31), and every pod of that build restarted after
// it would fail to pull too. Five outlasts such a burst; older ones go.
export const DEV_BUILDS_KEPT = 5;
// DRY_RUN is false: the first dry run's list was read against the registry —
// only dev builds and their children, no release, the newest kept.
// DELETE_TAGS_SEPARATOR joins the action's delete-tags patterns.
export const DELETE_TAGS_SEPARATOR = ",";
export const DRY_RUN = false;

// Output names the step outputs, which the cleanup action's inputs read.
export const Output = { Packages: "packages", DeleteTags: "delete_tags", Keep: "keep", DryRun: "dry_run" } as const;

// cleanupPlan is the action's inputs for the two packages.
export function cleanupPlan(image: string, chart: string): Record<string, string> {
  if (image === "" || chart === "") {
    throw new Error("both the image and the chart package are needed");
  }
  return {
    [Output.Packages]: `${image},${chart}`,
    // The action matches tags with shell-style wildcards, one pattern per
    // comma-separated entry.
    [Output.DeleteTags]: DEV_MARKS.map((mark) => `*${mark}*`).join(DELETE_TAGS_SEPARATOR),
    [Output.Keep]: String(DEV_BUILDS_KEPT),
    [Output.DryRun]: String(DRY_RUN),
  };
}

export const CLEANUP_TITLE = "### Older dev builds removed from GHCR";

// cleanupSummary is the summary page's line on what the cleanup step takes.
export function cleanupSummary(image: string, chart: string): string {
  const what = `Every dev build of \`${image}\` and \`${chart}\` but the newest ${DEV_BUILDS_KEPT}; releases are left alone.`;
  return `${CLEANUP_TITLE}\n\n${what}${DRY_RUN ? " A dry run: nothing is deleted." : ""}\n`;
}

async function main(): Promise<void> {
  const image = requireEnv("PACKAGE");
  const chart = requireEnv("CHART_PACKAGE");
  for (const [name, value] of Object.entries(cleanupPlan(image, chart))) {
    setOutput(name, value);
  }
  summarize(cleanupSummary(image, chart));
}

if (import.meta.main) {
  await runMain(main);
}
