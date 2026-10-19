import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { BRANCH, COMPOSE, FILE, GO_MOD, PACKAGE_JSON, nextVersion, publish, readVersions, render, type Versions } from "./badges.ts";
import { git } from "./lib/git.ts";
import { tempRepo, type TempRepo } from "./lib/temp-repo.ts";

const GO_MOD_TEXT = (cedar: boolean) =>
  [
    "module example.com/fixture",
    "",
    "go 1.30.1",
    "",
    "require (",
    "\tconnectrpc.com/connect/v2 v2.99.0",
    ...(cedar ? ["\tgithub.com/cedar-policy/cedar-go v1.9.1"] : []),
    "\tgithub.com/modelcontextprotocol/go-sdk v1.10.2",
    ")",
    "",
  ].join("\n");

const fixture = async (cedar: boolean, postgresTag = "18"): Promise<TempRepo> => {
  const repo = await tempRepo();
  repo.write(GO_MOD, GO_MOD_TEXT(cedar));
  repo.write(PACKAGE_JSON, '{"dependencies": {"next": "^17.0.1"}}\n');
  repo.write(COMPOSE, `x-base: &base\n  restart: "no"\nservices:\n  cache:\n    <<: *base\n    image: redis:8\n  postgres:\n    image: postgres:${postgresTag}\n`);
  return repo;
};

describe("readVersions", () => {
  test("each from the file that pins it", async () => {
    const repo = await fixture(true);
    try {
      assert.deepEqual(await readVersions(repo.dir), { go: "1.30.1", postgresql: "18", connect: "2.99.0", cedar: "1.9.1", mcp: "1.10.2", nextjs: "17.0.1" });
    } finally {
      repo.cleanup();
    }
  });

  // Renovate pins a digest after the tag; the badge reads the version, not it.
  test("a digest-pinned image", async () => {
    const repo = await fixture(true, "18.6@sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336");
    try {
      assert.equal((await readVersions(repo.dir)).postgresql, "18.6");
    } finally {
      repo.cleanup();
    }
  });

  test("a module missing from go.mod is an error, not a blank badge", async () => {
    const repo = await fixture(false);
    try {
      await assert.rejects(readVersions(repo.dir), /no version of github.com\/cedar-policy\/cedar-go/);
    } finally {
      repo.cleanup();
    }
  });

  test("this repository: every badge has a version", async () => {
    const versions = await readVersions(new URL("../..", import.meta.url).pathname);
    for (const [name, value] of Object.entries(versions)) {
      assert.match(value, /^[0-9]+(\.[0-9]+)*$/, name);
    }
  });
});

test("a range on next is not part of its version", () => {
  for (const range of ["17.0.1", "^17.0.1", "~17.0.1"]) {
    assert.equal(nextVersion(`{"dependencies": {"next": "${range}"}}`), "17.0.1", range);
  }
  assert.throws(() => nextVersion("{}"), /no version of next/);
});

describe("publish", () => {
  const versions: Versions = { go: "1", postgresql: "2", connect: "3", cedar: "4", mcp: "5", nextjs: "6" };

  // A clone of a bare origin, as the job's checkout is.
  const clone = async (): Promise<{ root: string; origin: string; cleanup(): void }> => {
    const base = mkdtempSync(join(tmpdir(), "paladin-ci-"));
    const origin = join(base, "origin.git");
    const seed = await tempRepo();
    await seed.commit("main");
    await git(["clone", "--quiet", "--bare", seed.dir, origin]);
    seed.cleanup();
    const root = join(base, "clone");
    await git(["clone", "--quiet", origin, root]);
    return { root, origin, cleanup: () => rmSync(base, { recursive: true, force: true }) };
  };

  const published = async (origin: string): Promise<{ file: string; commits: number; paths: string[] }> => ({
    file: await git(["show", `${BRANCH}:${FILE}`], origin),
    commits: Number((await git(["rev-list", "--count", BRANCH], origin)).trim()),
    paths: (await git(["ls-tree", "--name-only", BRANCH], origin)).trim().split("\n"),
  });

  test("creates the branch holding only the file, then commits only a change", async () => {
    const { root, origin, cleanup } = await clone();
    try {
      assert.equal(await publish(root, render(versions)), true);
      assert.deepEqual(await published(origin), { file: render(versions), commits: 1, paths: [FILE] });

      assert.equal(await publish(root, render(versions)), false);
      assert.equal((await published(origin)).commits, 1);

      const moved = render({ ...versions, go: "9" });
      assert.equal(await publish(root, moved), true);
      assert.deepEqual(await published(origin), { file: moved, commits: 2, paths: [FILE] });
      assert.equal((await git(["worktree", "list", "--porcelain"], root)).match(/^worktree /gm)?.length, 1);
    } finally {
      cleanup();
    }
  });
});
