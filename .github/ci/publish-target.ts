// Everything a component's image build and chart push need, read from the
// component's Taskfile and the task library rather than restated in the
// workflow, and the check that the version is one this publish may push.
//
// Usage: COMPONENT=<dir> KIND=<release|dev> TAG=<tag> REGISTRY=… IMAGE_NAMESPACE=… CHART_PATH=… node publish-target.ts
//
//   release  the version is the tag's, without its leading v
//   dev      the version is a prerelease of the next patch, <x.y.z>-dev.<…>,
//            or of the candidate it follows, <x.y.z>-rc.<n>.dev.<…>: either
//            sorts after the last tag and before the next
//
// A dev publish that derived a release version — a run that raced a tag —
// would push a release without the release run that announces it, so it is
// refused rather than published.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { parse } from "yaml";

import { setOutput } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { execute } from "./lib/exec.ts";
import { git } from "./lib/git.ts";
import { runMain } from "./lib/main.ts";
import { isDevVersion } from "./lib/versions.ts";
import { Kind } from "./publish-plan.ts";

// COMPONENT_TASKFILE is each component's Taskfile, under its directory.
const COMPONENT_TASKFILE = "Taskfile.yaml";
// VERSION_TASK is the library task that derives a component's version.
const VERSION_TASK = "release:version";
// SHORT_SHA is how much of the commit the image is labelled with: what the
// library's image:build passes.
export const SHORT_SHA = 8;
// TAG_PREFIX starts a product tag.
const TAG_PREFIX = "v";

// checkVersion refuses a version this kind of publish may not push.
export function checkVersion(kind: string, version: string, tag: string | undefined): void {
  if (version === "") {
    throw new Error("no version derived");
  }
  switch (kind) {
    case Kind.Release:
      if (tag === undefined || version !== tag.replace(new RegExp(`^${TAG_PREFIX}`), "")) {
        throw new Error(`the library derives ${version}, the tag is ${tag ?? "<none>"} — refusing to publish`);
      }
      return;
    case Kind.Dev:
      if (!isDevVersion(version)) {
        throw new Error(`a dev build derived ${version}, a release version — refusing to publish it`);
      }
      return;
    default:
      throw new Error(`unknown publish kind: ${kind || "<none>"}`);
  }
}

// Where is where a component publishes.
export interface Where {
  registry: string;
  namespace: string;
  chartPath: string;
}

// ComponentVars are the Taskfile variables the build reads.
interface ComponentVars {
  PROJECT_NAME?: string;
  CHART_NAME?: string;
  DOCKERFILE?: string;
  DOCKER_CONTEXT?: string;
}

const required = (component: string, name: string, value: string | undefined): string => {
  if (value === undefined || value === "") {
    throw new Error(`${component}/${COMPONENT_TASKFILE} sets no ${name}`);
  }
  return value;
};

// targets are the step outputs for component at version, its Taskfile text
// given.
export function targets(component: string, taskfile: string, version: string, where: Where): Record<string, string> {
  const vars: ComponentVars = (parse(taskfile) as { vars?: ComponentVars } | null)?.vars ?? {};
  const name = required(component, "PROJECT_NAME", vars.PROJECT_NAME);
  // The library names the chart CHART_NAME, else PROJECT_NAME.
  const chart = vars.CHART_NAME ?? name;
  return {
    version,
    image: `${where.registry}/${where.namespace}/${name}`,
    chart: `${where.registry}/${where.namespace}/${where.chartPath}/${chart}`,
    // The same two as GHCR package names, which carry no registry or owner.
    package: name,
    chart_package: `${where.chartPath}/${chart}`,
    file: `${component}/${required(component, "DOCKERFILE", vars.DOCKERFILE)}`,
    context: `${component}/${required(component, "DOCKER_CONTEXT", vars.DOCKER_CONTEXT)}`,
  };
}

// buildTime is when, as the library's image:build stamps it: UTC, to the
// second, with its offset.
export function buildTime(now: Date): string {
  return `${now.toISOString().slice(0, "YYYY-MM-DDTHH:MM:SS".length)}+0000`;
}

async function main(): Promise<void> {
  const component = requireEnv("COMPONENT");
  const kind = requireEnv("KIND");
  const root = (await git(["rev-parse", "--show-toplevel"])).trim();
  // --yes accepts the first-fetch trust prompt for the task library.
  const version = (await execute("task", ["--yes", `${component}:${VERSION_TASK}`], { cwd: root })).trim();
  checkVersion(kind, version, optionalEnv("TAG"));
  const outputs = targets(component, readFileSync(join(root, component, COMPONENT_TASKFILE), "utf8"), version, {
    registry: requireEnv("REGISTRY"),
    namespace: requireEnv("IMAGE_NAMESPACE"),
    chartPath: requireEnv("CHART_PATH"),
  });
  outputs.sha = (await git(["rev-parse", `--short=${SHORT_SHA}`, "HEAD"], root)).trim();
  outputs.build_time = buildTime(new Date());
  for (const [name, value] of Object.entries(outputs)) {
    setOutput(name, value);
  }
}

if (import.meta.main) {
  await runMain(main);
}
