// What this release run publishes.
//
// Usage: TAG=<product tag> EVENT=<event> SHA=<dispatched sha> node publish-plan.ts
//
// Sets the step outputs release.yaml's publish job reads:
//
//   kind=release ref=<tag>   a product tag was cut: publish the release at it
//   kind=dev     ref=<sha>   no tag, and CI dispatched this run for <sha>:
//                            publish a dev build of it, for the dev cluster
//   (nothing)                a manual run that cut no tag, or CI named no
//                            commit: publish nothing
//
// A dev build is every commit that passed CI on main, published so the dev
// cluster runs each change as it lands. CI dispatches a run only for a commit
// it passed, so the dispatch is the proof; the commit is built from its own
// sha, so it does not matter that main has moved on. It once did: the run
// built only a head that was still the dispatched commit, and a merge landing
// while that CI finished left the commit unpublished.

import { info, setOutput, summarize } from "./lib/actions.ts";
import { optionalEnv } from "./lib/env.ts";
import { runMain } from "./lib/main.ts";

// Kind is what the publish job builds.
export const Kind = { Release: "release", Dev: "dev" } as const;
export type Kind = (typeof Kind)[keyof typeof Kind];
// CI_DISPATCH is the event CI's dispatch-release job starts this run with.
export const CI_DISPATCH = "repository_dispatch";
// The step outputs.
export const Output = { Kind: "kind", Ref: "ref" } as const;

export interface Plan {
  kind: Kind;
  ref: string;
}

// plan decides from the tag this run cut, the event and the dispatched sha.
export function plan(tag: string | undefined, event: string | undefined, sha: string | undefined): Plan | undefined {
  if (tag !== undefined) {
    return { kind: Kind.Release, ref: tag };
  }
  if (event === CI_DISPATCH && sha !== undefined) {
    return { kind: Kind.Dev, ref: sha };
  }
  return undefined;
}

export const PLAN_TITLE = "### What this run publishes";
// NOTHING says why a run publishes nothing.
export const NOTHING = "Nothing: no tag was cut, and CI dispatched no commit.";

// planSummary is the summary page's line on what the run publishes.
export function planSummary(decided: Plan | undefined): string {
  const what =
    decided === undefined
      ? NOTHING
      : decided.kind === Kind.Release
        ? `The release of \`${decided.ref}\`: every component's image and chart, signed, and its GitHub release.`
        : `A dev build of \`${decided.ref}\`, for the dev cluster.`;
  return `${PLAN_TITLE}\n\n${what}\n`;
}

async function main(): Promise<void> {
  const decided = plan(optionalEnv("TAG"), optionalEnv("EVENT"), optionalEnv("SHA"));
  summarize(planSummary(decided));
  if (decided === undefined) {
    info("nothing to publish: no tag, and CI dispatched no commit");
    return;
  }
  setOutput(Output.Kind, decided.kind);
  setOutput(Output.Ref, decided.ref);
}

if (import.meta.main) {
  await runMain(main);
}
