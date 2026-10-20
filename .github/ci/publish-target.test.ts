import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, test } from "node:test";

import { buildTime, checkVersion, targets } from "./publish-target.ts";

describe("checkVersion: a release its tag, a dev build a dev version", () => {
  const ok: [string, string, string, string | undefined][] = [
    ["a release at its tag", "release", "37.1.0", "v37.1.0"],
    ["a dev build", "dev", "37.1.1-dev.1792157052.gabcdef12", undefined],
  ];
  for (const [name, kind, version, tag] of ok) test(name, () => assert.doesNotThrow(() => checkVersion(kind, version, tag)));
  const refused: [string, string, string, string | undefined][] = [
    ["a release that is not its tag", "release", "37.1.1", "v37.1.0"],
    ["a release that derived a dev version", "release", "37.1.1-dev.1792.gabc", "v37.1.0"],
    ["a release with no tag", "release", "37.1.0", undefined],
    ["a dev build that derived a release version", "dev", "37.1.0", undefined],
    ["no version", "dev", "", undefined],
    ["an unknown kind", "nightly", "37.1.1-dev.1.gabc", undefined],
  ];
  for (const [name, kind, version, tag] of refused) test(name, () => assert.throws(() => checkVersion(kind, version, tag)));
});

const where = { registry: "ghcr.io", namespace: "o", chartPath: "charts" };
const taskfile = (component: string) => readFileSync(new URL(`../../${component}/Taskfile.yaml`, import.meta.url), "utf8");

test("the backend's image and chart, from its Taskfile", () => {
  assert.deepEqual(targets("backend", taskfile("backend"), "1.2.3", where), {
    version: "1.2.3",
    image: "ghcr.io/o/paladin-core",
    chart: "ghcr.io/o/charts/paladin-core",
    package: "paladin-core",
    chart_package: "charts/paladin-core",
    file: "backend/./deploy/Dockerfile",
    context: "backend/..",
  });
});

test("the console's, and a chart named apart from its image", () => {
  assert.equal(targets("frontend", taskfile("frontend"), "1.2.3", where).package, "paladin-console");
  const named = "vars:\n  PROJECT_NAME: img\n  CHART_NAME: chrt\n  DOCKERFILE: D\n  DOCKER_CONTEXT: .\n";
  assert.equal(targets("x", named, "1", where).chart_package, "charts/chrt");
  assert.throws(() => targets("x", "vars:\n  PROJECT_NAME: img\n", "1", where), /x\/Taskfile.yaml sets no DOCKERFILE/);
});

test("the build time is UTC to the second, with its offset", () => {
  assert.equal(buildTime(new Date(Date.UTC(2026, 9, 19, 22, 43, 25, 768))), "2026-10-19T22:43:25+0000");
});
