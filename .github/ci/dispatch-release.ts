// Dispatch release.yaml for the commit this CI run checked.
//
// Usage: GH_TOKEN=… GITHUB_REPOSITORY=… GITHUB_SHA=… GITHUB_RUN_ID=… [VERSION=…] node dispatch-release.ts
//
// workflow_run is the obvious trigger for the release and is refused by
// zizmor's dangerous-triggers audit, and GitHub starts no workflow run from
// a push GITHUB_TOKEN made; repository_dispatch is one of the documented
// exceptions that always create a run. The payload names the commit and this
// run; release.yaml releases that commit only while it is main's head
// (release-head.ts). Octokit tries a transient failure again, so one 5xx
// does not lose the release.
//
// VERSION is the dev build's version. release.yaml names its run after the
// label made of it (run-name), v39.2.1-dev (622e006): a run's name is fixed
// when it starts, before the run could derive the version itself, and an
// expression cannot shorten it. Without a dev version the label is the commit.

import { info, summarize, warning } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { gitHubFor, type GitHub } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";
import { devBuildLabel, isDevVersion } from "./lib/versions.ts";

// RELEASE_EVENT is the repository_dispatch type release.yaml runs on.
export const RELEASE_EVENT = "release";

// Payload names what release.yaml reads from client_payload.
export const Payload = { Sha: "sha", CIRunID: "ci_run_id", Label: "label" } as const;

// dispatchRelease asks for a release of sha, vouched for by CI run runId,
// whose dev build is version when it is one, labelled for the run's name.
export async function dispatchRelease(github: GitHub, sha: string, runId: string, version?: string): Promise<void> {
  if (version !== undefined && !isDevVersion(version)) {
    warning(`${version} is no dev build's version; the release run is named after the commit`);
  }
  const payload = { [Payload.Sha]: sha, [Payload.CIRunID]: runId, [Payload.Label]: devBuildLabel(sha, version) };
  await github.dispatch(RELEASE_EVENT, payload);
  info(`dispatched release for ${sha}`);
  summarize(dispatchSummary(payload[Payload.Label]));
}

export const DISPATCH_TITLE = "### Release dispatched";

// dispatchSummary is the summary page's line naming the release run this one
// started, as the Release workflow's run list names it.
export function dispatchSummary(label: string): string {
  return `${DISPATCH_TITLE}\n\nThe Release workflow publishes \`${label}\`; its run is named after it.\n`;
}

async function main(): Promise<void> {
  const github = gitHubFor(requireEnv("GH_TOKEN"), requireEnv("GITHUB_REPOSITORY"));
  await dispatchRelease(github, requireEnv("GITHUB_SHA"), requireEnv("GITHUB_RUN_ID"), optionalEnv("VERSION"));
}

if (import.meta.main) {
  await runMain(main);
}
