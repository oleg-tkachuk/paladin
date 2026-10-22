// Which change does this CI run check, and which Verify groups does it reach?
//
// A pull request names both ends of its diff (BASE, HEAD); a push names the
// branch's previous head (BEFORE) and its new one (AFTER). The step sets:
//
//   groups    the Verify groups reached, as a JSON array: the verify-* tasks
//             in Taskfile.dev.yaml, one CI job each, so a console change does
//             not wait on the backend's tests. `[]` for documentation only.
//   e2e       whether the Playwright suite runs.
//   deep      whether verify-deep runs.
//   relevant  false when every changed path is documentation, so the checks
//             after it report as skipped.
//
// A path feeds the groups whose code reads it. backend builds against sdk/go/
// through `replace`, and every stub is generated from proto/. backend tests
// read the resource names, Biscuit vocabulary and fixtures the SDKs share in
// sdk/testdata/. The Taskfiles, their cached includes, the contract scripts,
// the CI scripts and ci.yaml feed every group. Anything else that is not
// documentation reaches `repo`, which checks charts, images, the contract and
// the repository's own config: a path nobody has classified is checked by
// default, not skipped.
//
// A push that created the branch reports an all-zero BEFORE, a force-push can
// name a commit this clone does not have, and a run by hand names none at
// all. None of them is a diff, so each reaches everything.

import { info, setOutput, summarize, table } from "./lib/actions.ts";
import { optionalEnv } from "./lib/env.ts";
import { changedPaths, hasCommit } from "./lib/git.ts";
import { matchesAny } from "./lib/glob.ts";
import { runMain } from "./lib/main.ts";

// INERT are the paths that change nothing CI checks, as GitHub's path filters
// spell them. An EXCLUSION list, and the direction matters: a file type nobody
// has thought about yet is relevant by default. codeql.yaml's paths-ignore is
// this list; changes.test.ts fails when the two disagree.
export const INERT: readonly string[] = [
  "**/*.md",
  "docs/**",
  "LICENSE",
  "NOTICE",
  ".gitignore",
  "**/*.png",
  "**/*.jpg",
  "**/*.jpeg",
  "**/*.svg",
  "**/*.gif",
];

// SHARED are the paths every group reads: what decides how a group is
// checked, or what CI runs at all.
export const SHARED: readonly string[] = [
  "Taskfile*.yaml",
  ".task/**",
  "scripts/**",
  ".github/ci/**",
  ".github/actions/**",
  ".github/workflows/ci.yaml",
];

// Group names a verify-* task of Taskfile.dev.yaml, and a Verify job.
export const Group = {
  Backend: "backend",
  Sdk: "sdk",
  Frontend: "frontend",
  Repo: "repo",
} as const;
export type Group = (typeof Group)[keyof typeof Group];

// ALL_GROUPS is every group, in the order the jobs are listed.
export const ALL_GROUPS: readonly Group[] = Object.values(Group);

// READERS are the paths each group but repo reads; repo reads every relevant
// path.
const READERS: Readonly<Record<Exclude<Group, typeof Group.Repo>, readonly string[]>> = {
  backend: ["backend/**", "sdk/go/**", "sdk/testdata/**", "proto/**"],
  sdk: ["sdk/**", "proto/**"],
  frontend: ["frontend/**", "proto/**"],
};

// E2E_GROUPS reach the Playwright suite: it drives the console against the
// backend, so a change either of them reaches is one it can break.
export const E2E_GROUPS: readonly Group[] = [Group.Backend, Group.Frontend];
// DEEP_GROUPS reach verify-deep, the Postgres-backed suites and the stack
// gate: backend code, and the SDKs, whose scenarios the stack gate runs
// against the server. The contract reaches it through the backend group.
export const DEEP_GROUPS: readonly Group[] = [Group.Backend, Group.Sdk];

// The step outputs ci.yaml reads.
export const Output = { Groups: "groups", E2e: "e2e", Deep: "deep", Relevant: "relevant" } as const;

// ZERO_SHA is the BEFORE of a push that created its branch.
const ZERO_SHA = "0".repeat(40);

// Range is the two commits a diff runs between.
export interface Range {
  from: string;
  to: string;
}

// DiffEnv is what the event says about the change.
export interface DiffEnv {
  base?: string | undefined;
  head?: string | undefined;
  before?: string | undefined;
  after?: string | undefined;
}

// reachedGroups is every group a change of paths reaches, in ALL_GROUPS'
// order.
export function reachedGroups(paths: readonly string[]): Group[] {
  const relevant = paths.filter((path) => path !== "" && !matchesAny(path, INERT));
  if (relevant.length === 0) {
    return [];
  }
  if (relevant.some((path) => matchesAny(path, SHARED))) {
    return [...ALL_GROUPS];
  }
  return ALL_GROUPS.filter((group) => group === Group.Repo || relevant.some((path) => matchesAny(path, READERS[group])));
}

// reachesAny reports whether groups holds one of wanted.
export function reachesAny(groups: readonly Group[], wanted: readonly Group[]): boolean {
  return groups.some((group) => wanted.includes(group));
}

// candidateRange is the diff the event names: a pull request's ends win over a
// push's. Undefined when it names no two ends.
export function candidateRange(env: DiffEnv): Range | undefined {
  const [from, to] = env.base !== undefined ? [env.base, env.head] : [env.before, env.after];
  if (from === undefined || to === undefined || from === ZERO_SHA) {
    return undefined;
  }
  return { from, to };
}

// classify is the groups the change reaches: of every path of the diff the
// event names, or every group when there is no diff this clone can take.
export async function classify(env: DiffEnv, cwd?: string): Promise<Group[]> {
  const range = candidateRange(env);
  if (range === undefined || !(await hasCommit(range.from, cwd))) {
    info(`no usable base (${range?.from ?? env.before ?? "none"}) — running everything`);
    return [...ALL_GROUPS];
  }
  const paths = await changedPaths(range.from, range.to, cwd);
  info(paths.join("\n"));
  return reachedGroups(paths);
}

// What the summary says of a check: it runs on this change, or reports as
// skipped.
export const RUNS = "runs";
export const SKIPPED = "skipped";
export const CHANGES_TITLE = "### What this change checks";
const CHANGES_HEADER = ["Check", "On this change"];
// The jobs that read each output, by their names in ci.yaml.
const VERIFY_JOB = (group: Group) => `Verify (${group})`;
const E2E_JOB = "End-to-end";
const DEEP_JOB = "Deep";

// changesSummary is the summary page's table of the checks this change
// reaches: a Verify group each, then end-to-end and the deep checks.
export function changesSummary(groups: readonly Group[], e2e: boolean, deep: boolean): string {
  const runs = (on: boolean) => (on ? RUNS : SKIPPED);
  const rows = [
    ...ALL_GROUPS.map((group) => [VERIFY_JOB(group), runs(groups.includes(group))]),
    [E2E_JOB, runs(e2e)],
    [DEEP_JOB, runs(deep)],
  ];
  const note = groups.length === 0 ? "\nDocumentation only: every check reports as skipped.\n" : "";
  return `${CHANGES_TITLE}\n\n${table(CHANGES_HEADER, rows)}${note}`;
}

async function main(): Promise<void> {
  const groups = await classify({
    base: optionalEnv("BASE"),
    head: optionalEnv("HEAD"),
    before: optionalEnv("BEFORE"),
    after: optionalEnv("AFTER"),
  });
  const e2e = reachesAny(groups, E2E_GROUPS);
  const deep = reachesAny(groups, DEEP_GROUPS);
  info(`groups: ${JSON.stringify(groups)}, end-to-end: ${e2e}, deep: ${deep}`);
  if (groups.length === 0) {
    info("documentation only — the checks below will report as skipped");
  }
  setOutput(Output.Groups, JSON.stringify(groups));
  setOutput(Output.E2e, String(e2e));
  setOutput(Output.Deep, String(deep));
  setOutput(Output.Relevant, String(groups.length > 0));
  summarize(changesSummary(groups, e2e, deep));
}

if (import.meta.main) {
  await runMain(main);
}
