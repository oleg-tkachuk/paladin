import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import { ConnectError, Code } from "@connectrpc/connect";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ getMine: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  userSettingsClient: { getMine: h.getMine },
}));

import { fetchMySettings, isTheme, THEME_OPTIONS } from "./theme";

const MIGRATIONS = join(__dirname, "..", "..", "..", "backend", "migrations");
const THEME_CHECK = /CHECK \(theme IN \(([^)]*)\)\)/;
const GOOSE_DOWN = "-- +goose Down";

/** The theme set of the newest migration that declares the CHECK. */
function databaseThemes(): string[] {
  let latest: string[] = [];
  for (const name of readdirSync(MIGRATIONS)
    .filter((n) => n.endsWith(".sql"))
    .sort()) {
    const up = readFileSync(join(MIGRATIONS, name), "utf8").split(
      GOOSE_DOWN,
    )[0];
    const m = up.match(THEME_CHECK);
    if (m) latest = m[1].split(",").map((v) => v.trim().replace(/'/g, ""));
  }
  return latest;
}

describe("themes", () => {
  // A theme the picker offers and the database refuses fails on Save; one
  // the database allows and the picker lacks cannot be chosen.
  it("offers exactly the themes the database accepts", () => {
    const db = databaseThemes();
    expect(db.length).toBeGreaterThan(0);
    expect(THEME_OPTIONS.map((o) => o.value).sort()).toEqual(db.sort());
  });

  it("recognises only the offered themes", () => {
    expect(isTheme("violet")).toBe(true);
    expect(isTheme("system")).toBe(true);
    expect(isTheme("midnight")).toBe(false);
    expect(isTheme("")).toBe(false);
  });
});

describe("fetchMySettings", () => {
  beforeEach(() => {
    h.getMine.mockReset();
  });

  it("returns the settings", async () => {
    h.getMine.mockResolvedValue({ theme: "violet" });
    await expect(fetchMySettings()).resolves.toEqual({ theme: "violet" });
  });

  it("returns null for a user who never saved any", async () => {
    h.getMine.mockRejectedValue(new ConnectError("no row", Code.NotFound));
    await expect(fetchMySettings()).resolves.toBeNull();
  });

  it("rethrows any other failure", async () => {
    const err = new ConnectError("down", Code.Unavailable);
    h.getMine.mockRejectedValue(err);
    await expect(fetchMySettings()).rejects.toBe(err);
  });
});
