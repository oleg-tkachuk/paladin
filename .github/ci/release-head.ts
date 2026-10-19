// May this run release the checked-out head?
//
// Usage: EVENT=<event> SHA=<dispatched sha> GH_TOKEN=… GITHUB_REPOSITORY=… node release-head.ts
//
// Sets the step output `release`. release.yaml checks out main's head,
// because semantic-release releases only from a branch, and two triggers
// reach it:
//
//   repository_dispatch  CI dispatched it for the commit it passed, SHA. The
//                        head can be newer: a pull request merged while that
//                        CI finished, and tagging it would release a commit
//                        whose own checks had not passed — that happened with
//                        v4.12.0. So only that commit, while it is the head;
//                        a later tag counts its changes, and its dev build is
//                        published all the same, from SHA (publish-plan.ts).
//   workflow_dispatch    an operator asked for a release (ADR-0034): the head,
//                        once a push run of ci.yaml on main passed on it. A
//                        run still in progress is not a pass.
//
// An empty SHA on a dispatch is an error, not a pass: the workflow cannot tell
// which commit CI vouched for.

import { info, setOutput } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { git } from "./lib/git.ts";
import { gitHubFor, type GitHub } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";

// OUTPUT_RELEASE is the step output release.yaml's later steps read.
export const OUTPUT_RELEASE = "release";
// MANUAL_EVENT is an operator's run; any other is CI's dispatch.
export const MANUAL_EVENT = "workflow_dispatch";
// CI_WORKFLOW, on RELEASE_BRANCH, is the run whose pass a release needs.
export const CI_WORKFLOW = "ci.yaml";
export const RELEASE_BRANCH = "main";
const PASSED = "success";

// resolveCommit is the full sha name resolves to in the clone at cwd, or
// undefined when it has no such commit.
async function resolveCommit(name: string, cwd?: string): Promise<string | undefined> {
  try {
    return (await git(["rev-parse", "--verify", "--quiet", `${name}^{commit}`], cwd)).trim();
  } catch {
    return undefined;
  }
}

// isHead reports whether sha, the commit CI passed, is the checked-out head.
export async function isHead(sha: string, cwd?: string): Promise<boolean> {
  if (sha === "") {
    throw new Error("no dispatched sha: cannot tell which commit CI passed");
  }
  const head = (await git(["rev-parse", "HEAD"], cwd)).trim();
  if ((await resolveCommit(sha, cwd)) === head) {
    return true;
  }
  info(`main moved past ${sha} to ${head}; that commit's own CI releases it`);
  return false;
}

// ciPassed reports whether a push run of ci.yaml on main passed on sha.
export async function ciPassed(github: GitHub, sha: string): Promise<boolean> {
  if (sha === "") {
    throw new Error("no sha: cannot tell which commit to check");
  }
  const conclusions = await github.conclusionsOf(CI_WORKFLOW, RELEASE_BRANCH, sha);
  if (conclusions.includes(PASSED)) {
    return true;
  }
  info(`CI has not passed on ${sha} (runs: ${conclusions.join(", ") || "none"}); release it once it has`);
  return false;
}

// releasable decides for the event that started the run.
export async function releasable(event: string, sha: string, github: () => GitHub, cwd?: string): Promise<boolean> {
  if (event === MANUAL_EVENT) {
    return ciPassed(github(), (await git(["rev-parse", "HEAD"], cwd)).trim());
  }
  return isHead(sha, cwd);
}

async function main(): Promise<void> {
  const release = await releasable(requireEnv("EVENT"), optionalEnv("SHA") ?? "", () =>
    gitHubFor(requireEnv("GH_TOKEN"), requireEnv("GITHUB_REPOSITORY")),
  );
  setOutput(OUTPUT_RELEASE, String(release));
}

if (import.meta.main) {
  await runMain(main);
}
