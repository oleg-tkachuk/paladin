// The versions of the stack the README's badges show, read from the files
// that pin them, and their publication to the `badges` branch.
//
// Usage: node badges.ts versions      print them as JSON
//        node badges.ts publish       commit them to the badges branch and push it
//
//   go, connect, cedar, mcp   backend/go.mod, through `go mod edit -json`
//   nextjs                    frontend/package.json
//   postgresql                the server image in backend/deploy/docker-compose.yaml
//
// shields.io reads JSON, YAML and TOML, but not go.mod, so the versions are
// published as one JSON file on a branch of its own, which holds only that
// file; main is never written. A version missing from its file is an error,
// not a blank badge.

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { parse } from "yaml";

import { info } from "./lib/actions.ts";
import { execute } from "./lib/exec.ts";
import { git } from "./lib/git.ts";
import { runMain } from "./lib/main.ts";

// The files the versions are read from, relative to the repository root.
export const GO_MOD = "backend/go.mod";
export const PACKAGE_JSON = "frontend/package.json";
export const COMPOSE = "backend/deploy/docker-compose.yaml";

// The modules whose versions are shown.
const MOD_CONNECT = "connectrpc.com/connect/v2";
const MOD_CEDAR = "github.com/cedar-policy/cedar-go";
const MOD_MCP = "github.com/modelcontextprotocol/go-sdk";
// POSTGRES_IMAGE is the image Paladin's own stack runs. "postgres:18.6" is
// 18.6, and a digest Renovate pins after the tag is not part of it.
const POSTGRES_IMAGE = "postgres";

// BRANCH holds the published file, FILE; the commit is the bot's.
export const BRANCH = "badges";
export const FILE = "versions.json";
const BOT = ["-c", "user.name=github-actions[bot]", "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com"];
const COMMIT_MESSAGE = "chore(badges): publish the stack versions";
const REMOTE = "origin";

// Command is what the step asks for.
export const Command = { Versions: "versions", Publish: "publish" } as const;

// Versions are the badges' values, in the order the JSON lists them.
export interface Versions {
  go: string;
  postgresql: string;
  connect: string;
  cedar: string;
  mcp: string;
  nextjs: string;
}

// GoMod is what `go mod edit -json` prints of a go.mod.
export interface GoMod {
  Go?: string;
  Require?: { Path: string; Version: string }[];
}

const required = (what: string, value: string | undefined): string => {
  if (value === undefined || value === "") {
    throw new Error(`no version of ${what}`);
  }
  return value;
};

// moduleVersion is the version goMod requires of path, without its v.
export function moduleVersion(goMod: GoMod, path: string): string {
  return required(path, goMod.Require?.find((r) => r.Path === path)?.Version.replace(/^v/, ""));
}

// nextVersion is the Next.js version package.json pins, without a range.
export function nextVersion(packageJson: string): string {
  const pkg = JSON.parse(packageJson) as { dependencies?: Record<string, string> };
  return required("next", pkg.dependencies?.next?.replace(/^[\^~]/, ""));
}

// postgresVersion is the tag of the first postgres image compose runs.
export function postgresVersion(compose: string): string {
  const doc = parse(compose, { merge: true }) as { services?: Record<string, { image?: string }> };
  const prefix = `${POSTGRES_IMAGE}:`;
  const image = Object.values(doc.services ?? {}).find((s) => s.image?.startsWith(prefix))?.image;
  return required(POSTGRES_IMAGE, image?.slice(prefix.length).split("@")[0]);
}

// readVersions reads every badge's version under root.
export async function readVersions(root: string): Promise<Versions> {
  const goMod = JSON.parse(await execute("go", ["mod", "edit", "-json", join(root, GO_MOD)])) as GoMod;
  return {
    go: required("go", goMod.Go),
    postgresql: postgresVersion(readFileSync(join(root, COMPOSE), "utf8")),
    connect: moduleVersion(goMod, MOD_CONNECT),
    cedar: moduleVersion(goMod, MOD_CEDAR),
    mcp: moduleVersion(goMod, MOD_MCP),
    nextjs: nextVersion(readFileSync(join(root, PACKAGE_JSON), "utf8")),
  };
}

// render is the published file.
export function render(versions: Versions): string {
  return `${JSON.stringify(versions, null, 2)}\n`;
}

// publish commits content as FILE on BRANCH of the clone at root, and pushes
// it, unless the branch holds it already. It reports whether it pushed.
export async function publish(root: string, content: string): Promise<boolean> {
  const dir = mkdtempSync(join(tmpdir(), "paladin-badges-"));
  rmSync(dir, { recursive: true, force: true });
  let fetched = true;
  try {
    await git(["fetch", "--quiet", REMOTE, BRANCH], root);
  } catch {
    fetched = false;
  }
  try {
    if (fetched) {
      await git(["worktree", "add", "--quiet", dir, "FETCH_HEAD"], root);
      await git(["switch", "--quiet", "-C", BRANCH], dir);
    } else {
      await git(["worktree", "add", "--quiet", "--detach", dir], root);
      await git(["switch", "--quiet", "--orphan", BRANCH], dir);
    }
    writeFileSync(join(dir, FILE), content);
    await git(["add", FILE], dir);
    if ((await git(["diff", "--cached", "--name-only"], dir)).trim() === "") {
      info("badge versions unchanged");
      return false;
    }
    await git([...BOT, "commit", "--quiet", "-m", COMMIT_MESSAGE], dir);
    await git(["push", "--quiet", REMOTE, BRANCH], dir);
    info(content);
    return true;
  } finally {
    await git(["worktree", "remove", "--force", dir], root).catch(() => undefined);
  }
}

async function main(): Promise<void> {
  const root = (await git(["rev-parse", "--show-toplevel"])).trim();
  const [command] = process.argv.slice(2);
  switch (command) {
    case Command.Versions:
      process.stdout.write(render(await readVersions(root)));
      break;
    case Command.Publish:
      await publish(root, render(await readVersions(root)));
      break;
    default:
      throw new Error(`usage: badges.ts ${Command.Versions}|${Command.Publish}`);
  }
}

if (import.meta.main) {
  await runMain(main);
}
