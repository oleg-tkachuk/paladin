// Merge automation: queue a pull request to merge once it is green, and bring
// every open pull request that is behind main up to date with it.
//
// Usage: GH_TOKEN=… GITHUB_REPOSITORY=… PR=<number> node auto-merge.ts enable
//        GH_TOKEN=… GITHUB_REPOSITORY=… node auto-merge.ts update
//
// GH_TOKEN is AUTOMERGE_TOKEN: GitHub starts no workflow for a push
// GITHUB_TOKEN makes, so a branch it updated would never be checked. Unset,
// the step warns and does nothing rather than fail every pull request.

import { info, summarize, warning } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { MergeState, gitHubFor, type GitHub, type OpenPullRequest } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";

// BASE is the branch pull requests merge into.
export const BASE = "main";

// Command is what the step asks for.
export const Command = { Enable: "enable", Update: "update" } as const;

// updatable reports whether updating the branch can make p mergeable: a
// ready pull request from this repository that is only behind its base. A
// conflict needs a person, and a fork's branch is not this token's to push.
export function updatable(p: OpenPullRequest): boolean {
  return !p.draft && !p.fromFork && p.mergeState === MergeState.Behind;
}

// updateBehind updates every updatable pull request into BASE. One that
// cannot be updated — a conflict GitHub only finds while updating — is
// warned about and does not stop the rest.
export async function updateBehind(github: GitHub): Promise<number[]> {
  const updated: number[] = [];
  for (const p of (await github.openPullRequestsInto(BASE)).filter(updatable)) {
    try {
      await github.updateBranch(p.number);
      info(`updated #${p.number}`);
      updated.push(p.number);
    } catch (err) {
      warning(`could not update #${p.number}; it needs a manual update: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  return updated;
}

// Outcome is what queue did with a pull request.
export const Outcome = { Queued: "queued", Merged: "merged" } as const;
export type Outcome = (typeof Outcome)[keyof typeof Outcome];

// MERGEABLE_NOW are the states in which GitHub merges without waiting, and so
// refuses to queue a merge ("Pull request is in clean status").
const MERGEABLE_NOW: ReadonlySet<string> = new Set([MergeState.Clean, MergeState.Unstable, MergeState.HasHooks]);

export function mergeableNow(mergeState: string): boolean {
  return MERGEABLE_NOW.has(mergeState);
}

// queue queues a pull request to merge once it is green. One already green —
// a draft marked ready after its checks passed — GitHub will not queue, so it
// is merged now, pinned to the head whose state was read. Any other refusal
// is the caller's to see.
export async function queue(github: GitHub, number: number): Promise<Outcome> {
  try {
    await github.enableAutoMerge(number);
    return Outcome.Queued;
  } catch (err) {
    const state = await github.pullRequestState(number);
    if (!mergeableNow(state.mergeState)) {
      throw err;
    }
    info(`#${number} is ${state.mergeState} and cannot be queued; merging ${state.head} now`);
    await github.merge(number, state.head);
    return Outcome.Merged;
  }
}

// pullNumber reads a pull request number, refusing anything else.
export function pullNumber(text: string): number {
  if (!/^[1-9][0-9]*$/.test(text)) {
    throw new Error(`pull request ${JSON.stringify(text)} is not a number`);
  }
  return Number(text);
}

export const UPDATED_TITLE = "### Pull requests brought up to date with main";

// updatedSummary is the summary page's list of the pull requests updated.
export function updatedSummary(updated: readonly number[], repository: string): string {
  const what =
    updated.length === 0
      ? "None was behind."
      : updated.map((n) => `- [#${n}](https://github.com/${repository}/pull/${n})`).join("\n");
  return `${UPDATED_TITLE}\n\n${what}\n`;
}

async function main(): Promise<void> {
  const [command] = process.argv.slice(2);
  if (command !== Command.Enable && command !== Command.Update) {
    throw new Error(`usage: auto-merge.ts ${Command.Enable}|${Command.Update}`);
  }
  const token = optionalEnv("GH_TOKEN");
  if (token === undefined) {
    warning("AUTOMERGE_TOKEN is not set; nothing done (see auto-merge.yaml)");
    return;
  }
  const github = gitHubFor(token, requireEnv("GITHUB_REPOSITORY"));
  if (command === Command.Enable) {
    const number = pullNumber(requireEnv("PR"));
    if ((await queue(github, number)) === Outcome.Queued) {
      info(`#${number} merges once it is green`);
    } else {
      info(`#${number} merged`);
    }
  } else {
    summarize(updatedSummary(await updateBehind(github), requireEnv("GITHUB_REPOSITORY")));
  }
}

if (import.meta.main) {
  await runMain(main);
}
